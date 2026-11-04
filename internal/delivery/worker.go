package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/AmeenKhan12345/Eventrail/internal/secrets"
)

const (
	defaultConcurrency   = 10
	defaultPollInterval  = time.Second
	defaultLease         = 60 * time.Second
	defaultShutdownGrace = 15 * time.Second
	leaseMargin          = 10 * time.Second
	recordTimeout        = 5 * time.Second
)

// Store is the queue a worker claims from. *QueueStore implements it.
type Store interface {
	Claim(ctx context.Context, lease time.Duration) (*Claimed, error)
	RecordResult(ctx context.Context, claimed *Claimed, result Result, outcome Outcome) error
}

// WorkerConfig is how many deliveries to run at once and how long to keep each claim.
// Zero Concurrency, PollInterval, Lease, and ShutdownGrace use the defaults:
// 10 workers, poll every 1s, lease for 60s, and wait 15s on shutdown.
type WorkerConfig struct {
	Concurrency   int
	PollInterval  time.Duration
	Lease         time.Duration
	ShutdownGrace time.Duration
}

// Worker claims deliveries, sends the stored body, and records the result.
type Worker struct {
	store         Store
	box           *secrets.Box
	sender        *Sender
	concurrency   int
	pollInterval  time.Duration
	lease         time.Duration
	shutdownGrace time.Duration
	sendTimeout   time.Duration
	jitter        func() float64
}

// NewWorker checks the config. The lease must cover the send plus 10 seconds,
// so a slow response is still inside the claim when the result is recorded.
func NewWorker(store Store, box *secrets.Box, sender *Sender, cfg WorkerConfig) (*Worker, error) {
	if store == nil {
		return nil, fmt.Errorf("worker store is required")
	}
	if box == nil {
		return nil, fmt.Errorf("worker box is required")
	}
	if sender == nil {
		return nil, fmt.Errorf("worker sender is required")
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = defaultConcurrency
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.Lease == 0 {
		cfg.Lease = defaultLease
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = defaultShutdownGrace
	}
	if cfg.Concurrency < 1 {
		return nil, fmt.Errorf("worker concurrency must be at least 1")
	}
	if cfg.Lease < sender.Timeout()+leaseMargin {
		return nil, fmt.Errorf("worker lease must be at least the send timeout plus 10s")
	}
	return &Worker{
		store:         store,
		box:           box,
		sender:        sender,
		concurrency:   cfg.Concurrency,
		pollInterval:  cfg.PollInterval,
		lease:         cfg.Lease,
		shutdownGrace: cfg.ShutdownGrace,
		sendTimeout:   sender.Timeout(),
		jitter:        randomJitter,
	}, nil
}

// Run starts one goroutine per unit of concurrency. Each one claims, sends,
// and records until ctx is cancelled. Run then waits up to ShutdownGrace for
// the send that is already in flight. That send uses its own timeout, not ctx.
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for range w.concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx)
		}()
	}

	<-ctx.Done()

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	timer := time.NewTimer(w.shutdownGrace)
	defer timer.Stop()
	select {
	case <-finished:
	case <-timer.C:
	}
	return nil
}

func (w *Worker) loop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		claimed, err := w.store.Claim(ctx, w.lease)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("claim delivery", "err", err)
			if !w.wait(ctx) {
				return
			}
			continue
		}
		if claimed == nil {
			if !w.wait(ctx) {
				return
			}
			continue
		}
		w.process(claimed)
	}
}

func (w *Worker) wait(ctx context.Context) bool {
	timer := time.NewTimer(w.pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (w *Worker) process(claimed *Claimed) {
	if claimed.AttemptCount > 8 {
		w.record(claimed, Result{
			FailureKind: FailureAttemptsExhausted,
			ErrorText:   "attempt limit reached",
		}, Outcome{Kind: OutcomeFailure})
		return
	}
	if claimed.DeletedAt != nil || !claimed.IsActive {
		text := "endpoint is not active"
		if claimed.DeletedAt != nil {
			text = "endpoint is deleted"
		}
		w.record(claimed, Result{
			FailureKind: FailureEndpointUnavailable,
			ErrorText:   text,
		}, Outcome{Kind: OutcomeFailure})
		return
	}

	secret, err := w.box.Decrypt(claimed.SecretCiphertext)
	if err != nil {
		slog.Error("decrypt endpoint secret", "err", err, "delivery_id", claimed.DeliveryID)
		w.record(claimed, Result{
			FailureKind: FailureDecrypt,
			ErrorText:   "decrypt endpoint secret",
		}, Outcome{Kind: OutcomeFailure})
		return
	}

	// Background, not the Run ctx: cancelling the worker must not cut the POST.
	sendCtx, cancel := context.WithTimeout(context.Background(), w.sendTimeout)
	result := w.sender.Send(sendCtx, Request{
		DeliveryID: claimed.DeliveryID.String(),
		URL:        claimed.URL,
		Body:       []byte(claimed.WebhookBody),
		Secret:     secret,
	})
	cancel()

	outcome := Outcome{Kind: OutcomeSuccess}
	if result.FailureKind != FailureNone {
		outcome = w.failureOutcome(claimed.AttemptCount)
	}
	w.record(claimed, result, outcome)
}

func (w *Worker) failureOutcome(attemptsMade int) Outcome {
	delay, ok := NextDelay(attemptsMade, w.jitter)
	if !ok {
		return Outcome{Kind: OutcomeFailure}
	}
	return Outcome{Kind: OutcomeRetry, Delay: delay}
}

func (w *Worker) record(claimed *Claimed, result Result, outcome Outcome) {
	ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()
	err := w.store.RecordResult(ctx, claimed, result, outcome)
	if err == nil {
		return
	}
	if errors.Is(err, ErrStaleClaim) {
		slog.Info("stale delivery claim", "delivery_id", claimed.DeliveryID, "attempt", claimed.AttemptCount)
		return
	}
	slog.Error("record delivery result", "err", err, "delivery_id", claimed.DeliveryID, "attempt", claimed.AttemptCount)
}

func randomJitter() float64 {
	return rand.Float64()*2 - 1
}
