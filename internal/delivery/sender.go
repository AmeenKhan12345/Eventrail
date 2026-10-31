package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"syscall"
	"time"
)

const (
	defaultTimeout       = 10 * time.Second
	defaultExcerptBytes  = 2048
	maxErrorBytes        = 1024
	maxResponseHeaderBts = 64 << 10
	userAgent            = "Eventrail/1.0"
)

// FailureKind is the machine-readable reason a delivery did not succeed.
type FailureKind string

const (
	FailureNone               FailureKind = "none"
	FailureTimeout            FailureKind = "timeout"
	FailureNetwork            FailureKind = "network"
	FailureBlockedDestination FailureKind = "blocked_destination"
	FailureNon2xx             FailureKind = "non_2xx"
	FailureInvalidURL         FailureKind = "invalid_url"
)

// Config is the HTTP client settings for Sender.
type Config struct {
	Timeout            time.Duration
	AllowInsecure      bool
	MaxResponseExcerpt int
	Now                func() time.Time
}

// Request is one delivery attempt. Body is the JSON posted to the endpoint.
type Request struct {
	DeliveryID string
	URL        string
	Body       []byte
	Secret     string
}

// Result is the outcome of one attempt. StatusCode is 0 when there was no response.
type Result struct {
	StatusCode      int
	Latency         time.Duration
	ResponseExcerpt string
	ErrorText       string
	FailureKind     FailureKind
}

// Sender posts a signed webhook. It does not read a database or retry.
type Sender struct {
	client        *http.Client
	now           func() time.Time
	allowInsecure bool
	maxExcerpt    int
}

// NewSender builds a client that dials directly and checks the destination
// address with policy at connect time. A nil policy uses DefaultPolicy.
func NewSender(cfg Config, policy IPPolicy) *Sender {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxResponseExcerpt <= 0 {
		cfg.MaxResponseExcerpt = defaultExcerptBytes
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if policy == nil {
		policy = DefaultPolicy
	}

	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: cfg.Timeout,
			Control: controlDestination(policy),
		}).DialContext,
		ResponseHeaderTimeout:  cfg.Timeout,
		MaxResponseHeaderBytes: maxResponseHeaderBts,
	}
	return &Sender{
		client: &http.Client{
			Timeout:       cfg.Timeout,
			Transport:     transport,
			CheckRedirect: refuseRedirect,
		},
		now:           cfg.Now,
		allowInsecure: cfg.AllowInsecure,
		maxExcerpt:    cfg.MaxResponseExcerpt,
	}
}

// Send posts req once. Any 2xx is success. The call stops when ctx is canceled.
func (s *Sender) Send(ctx context.Context, req Request) Result {
	started := time.Now()
	finish := func(result Result) Result {
		result.Latency = time.Since(started)
		result.ErrorText = clip(result.ErrorText, maxErrorBytes)
		result.ResponseExcerpt = clip(result.ResponseExcerpt, s.maxExcerpt)
		return result
	}
	if ctx == nil {
		return finish(Result{FailureKind: FailureNetwork, ErrorText: "context is required"})
	}
	if err := validateURL(req.URL, s.allowInsecure); err != nil {
		return finish(Result{FailureKind: FailureInvalidURL, ErrorText: err.Error()})
	}

	now := s.now()
	signature, err := Sign(req.Secret, req.DeliveryID, now, req.Body)
	if err != nil {
		return finish(Result{FailureKind: FailureNetwork, ErrorText: err.Error()})
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return finish(Result{FailureKind: FailureInvalidURL, ErrorText: err.Error()})
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)
	httpReq.Header.Set("webhook-id", req.DeliveryID)
	httpReq.Header.Set("webhook-timestamp", strconv.FormatInt(now.Unix(), 10))
	httpReq.Header.Set("webhook-signature", signature)

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return finish(Result{FailureKind: classify(err), ErrorText: err.Error()})
	}
	defer resp.Body.Close()

	excerpt, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(s.maxExcerpt)))
	result := Result{StatusCode: resp.StatusCode, ResponseExcerpt: string(excerpt)}
	if readErr != nil {
		result.FailureKind = FailureNetwork
		result.ErrorText = readErr.Error()
		return finish(result)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.FailureKind = FailureNon2xx
		result.ErrorText = fmt.Sprintf("status %d", resp.StatusCode)
		return finish(result)
	}
	result.FailureKind = FailureNone
	return finish(result)
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func validateURL(raw string, allowInsecure bool) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("url is invalid")
	}
	if parsed.Host == "" {
		return fmt.Errorf("url host is required")
	}
	if parsed.User != nil {
		return fmt.Errorf("url must not include user info")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if allowInsecure {
			return nil
		}
	}
	return fmt.Errorf("url scheme must be https")
}

func controlDestination(policy IPPolicy) func(string, string, syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("%w: %s", errDestinationBlocked, err.Error())
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("%w: %s", errDestinationBlocked, err.Error())
		}
		return policy(addr)
	}
}

func classify(err error) FailureKind {
	if errors.Is(err, errDestinationBlocked) {
		return FailureBlockedDestination
	}
	if isTimeout(err) {
		return FailureTimeout
	}
	return FailureNetwork
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func clip(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes]
}
