package delivery

import (
	"testing"
	"time"
)

func TestNextDelay(t *testing.T) {
	exact := func() float64 { return 0 }
	shorter := func() float64 { return -1 }
	longer := func() float64 { return 1 }
	clamped := func() float64 { return 5 }
	panicJitter := func() float64 {
		t.Fatal("jitter called after the schedule ended")
		return 0
	}

	tests := []struct {
		name     string
		attempts int
		jitter   func() float64
		want     time.Duration
		ok       bool
	}{
		{name: "after 1", attempts: 1, jitter: exact, want: 30 * time.Second, ok: true},
		{name: "after 1 shorter", attempts: 1, jitter: shorter, want: 24 * time.Second, ok: true},
		{name: "after 1 longer", attempts: 1, jitter: longer, want: 36 * time.Second, ok: true},
		{name: "after 1 clamped", attempts: 1, jitter: clamped, want: 36 * time.Second, ok: true},
		{name: "after 2", attempts: 2, jitter: exact, want: 2 * time.Minute, ok: true},
		{name: "after 3", attempts: 3, jitter: exact, want: 10 * time.Minute, ok: true},
		{name: "after 4", attempts: 4, jitter: exact, want: 30 * time.Minute, ok: true},
		{name: "after 5", attempts: 5, jitter: exact, want: 2 * time.Hour, ok: true},
		{name: "after 6", attempts: 6, jitter: exact, want: 6 * time.Hour, ok: true},
		{name: "after 7", attempts: 7, jitter: exact, want: 12 * time.Hour, ok: true},
		{name: "after 7 longer", attempts: 7, jitter: longer, want: 12 * time.Hour * 120 / 100, ok: true},
		{name: "zero attempts", attempts: 0, jitter: panicJitter, ok: false},
		{name: "eighth attempt", attempts: 8, jitter: panicJitter, ok: false},
		{name: "past the schedule", attempts: 9, jitter: panicJitter, ok: false},
		{name: "missing jitter", attempts: 1, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NextDelay(tt.attempts, tt.jitter)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("NextDelay(%d) = %s, %v; want %s, %v", tt.attempts, got, ok, tt.want, tt.ok)
			}
		})
	}
}
