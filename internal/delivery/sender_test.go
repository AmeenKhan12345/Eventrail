package delivery

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestSenderBlocksLoopbackBeforeDial(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer server.Close()

	sender := NewSender(Config{AllowInsecure: true, Timeout: time.Second}, DefaultPolicy)
	result := sender.Send(context.Background(), Request{
		DeliveryID: "del_1",
		URL:        server.URL,
		Body:       []byte(`{"n":1}`),
		Secret:     testSecret(t, 7),
	})
	if result.FailureKind != FailureBlockedDestination {
		t.Fatalf("kind = %s, error %s", result.FailureKind, result.ErrorText)
	}
	if result.StatusCode != 0 {
		t.Fatalf("status = %d", result.StatusCode)
	}
	if hits.Load() != 0 {
		t.Fatalf("server received %d requests", hits.Load())
	}
}

func TestSenderPostsAndVerifies(t *testing.T) {
	secret := testSecret(t, 7)
	body := []byte(`{"n":1}`)
	var gotID, gotTimestamp, gotSignature, gotType, gotAgent string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("webhook-id")
		gotTimestamp = r.Header.Get("webhook-timestamp")
		gotSignature = r.Header.Get("webhook-signature")
		gotType = r.Header.Get("Content-Type")
		gotAgent = r.Header.Get("User-Agent")
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sender := NewSender(Config{AllowInsecure: true, Timeout: time.Second}, PermissivePolicy)
	result := sender.Send(context.Background(), Request{DeliveryID: "del_1", URL: server.URL, Body: body, Secret: secret})
	if result.FailureKind != FailureNone || result.StatusCode != http.StatusNoContent {
		t.Fatalf("result = %+v", result)
	}
	if gotType != "application/json" || gotAgent != "Eventrail/1.0" || gotID != "del_1" {
		t.Fatalf("headers type %q agent %q id %q", gotType, gotAgent, gotID)
	}
	if err := Verify(secret, gotID, gotTimestamp, gotSignature, gotBody, time.Now(), time.Minute); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("body = %s", gotBody)
	}
}

func TestSenderNon2xxKeepsExcerpt(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "500", status: http.StatusInternalServerError, body: "server failed"},
		{name: "404", status: http.StatusNotFound, body: "missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			sender := NewSender(Config{AllowInsecure: true, Timeout: time.Second}, PermissivePolicy)
			result := sender.Send(context.Background(), Request{
				DeliveryID: "del_1",
				URL:        server.URL,
				Body:       []byte(`{}`),
				Secret:     testSecret(t, 7),
			})
			if result.FailureKind != FailureNon2xx || result.StatusCode != tt.status {
				t.Fatalf("result = %+v", result)
			}
			if result.ResponseExcerpt != tt.body {
				t.Fatalf("excerpt = %q", result.ResponseExcerpt)
			}
		})
	}
}

func TestSenderTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
		case <-timer.C:
		}
	}))
	defer server.Close()

	sender := NewSender(Config{AllowInsecure: true, Timeout: 100 * time.Millisecond}, PermissivePolicy)
	result := sender.Send(context.Background(), Request{
		DeliveryID: "del_1",
		URL:        server.URL,
		Body:       []byte(`{}`),
		Secret:     testSecret(t, 7),
	})
	if result.FailureKind != FailureTimeout || result.StatusCode != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestSenderDoesNotFollowRedirect(t *testing.T) {
	var hits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer internal.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redirect.Close()

	sender := NewSender(Config{AllowInsecure: true, Timeout: time.Second}, PermissivePolicy)
	result := sender.Send(context.Background(), Request{
		DeliveryID: "del_1",
		URL:        redirect.URL,
		Body:       []byte(`{}`),
		Secret:     testSecret(t, 7),
	})
	if result.FailureKind != FailureNon2xx || result.StatusCode != http.StatusFound {
		t.Fatalf("result = %+v", result)
	}
	if hits.Load() != 0 {
		t.Fatal("redirect target was requested")
	}
}

func TestSenderCapsResponseExcerpt(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 10<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	sender := NewSender(Config{AllowInsecure: true, Timeout: time.Second}, PermissivePolicy)
	result := sender.Send(context.Background(), Request{
		DeliveryID: "del_1",
		URL:        server.URL,
		Body:       []byte(`{}`),
		Secret:     testSecret(t, 7),
	})
	if result.FailureKind != FailureNone {
		t.Fatalf("result = %+v", result)
	}
	if len(result.ResponseExcerpt) > defaultExcerptBytes {
		t.Fatalf("excerpt length = %d", len(result.ResponseExcerpt))
	}
}

func TestSenderTimestampsChangeWithClock(t *testing.T) {
	stamps := []time.Time{time.Unix(1_700_000_000, 0), time.Unix(1_700_000_100, 0)}
	var calls atomic.Int32
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("webhook-timestamp"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := NewSender(Config{
		AllowInsecure: true,
		Timeout:       time.Second,
		Now: func() time.Time {
			n := int(calls.Add(1)) - 1
			return stamps[n]
		},
	}, PermissivePolicy)
	for i := 0; i < 2; i++ {
		result := sender.Send(context.Background(), Request{
			DeliveryID: "del_1",
			URL:        server.URL,
			Body:       []byte(`{}`),
			Secret:     testSecret(t, 7),
		})
		if result.FailureKind != FailureNone {
			t.Fatalf("send %d: %+v", i, result)
		}
	}
	if len(got) != 2 || got[0] == got[1] || got[0] != "1700000000" || got[1] != "1700000100" {
		t.Fatalf("timestamps = %v", got)
	}
}

func TestSenderRejectsURL(t *testing.T) {
	sender := NewSender(Config{Timeout: time.Second}, PermissivePolicy)
	insecure := NewSender(Config{AllowInsecure: true, Timeout: time.Second}, PermissivePolicy)
	tests := []struct {
		name   string
		sender *Sender
		url    string
	}{
		{name: "http", sender: sender, url: "http://example.com/hook"},
		{name: "userinfo", sender: insecure, url: "http://user:pass@127.0.0.1/hook"},
		{name: "missing host", sender: insecure, url: "http:///hook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.sender.Send(context.Background(), Request{
				DeliveryID: "del_1",
				URL:        tt.url,
				Body:       []byte(`{}`),
				Secret:     testSecret(t, 7),
			})
			if result.FailureKind != FailureInvalidURL || result.StatusCode != 0 {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}
