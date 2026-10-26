package kiro

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
)

// fakeClock drives the limiter without real waiting. advance models requests
// that arrive one after another; no advance models requests that are already
// in flight and therefore pile up their slots.
type fakeClock struct {
	now     time.Time
	advance bool
	slept   []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), advance: true}
}

func (c *fakeClock) concurrent() *fakeClock {
	c.advance = false
	return c
}

func (c *fakeClock) install(t *testing.T) *fakeClock {
	t.Helper()
	limiter.mu.Lock()
	limiter.states = make(map[string]*credentialState)
	limiter.now = func() time.Time { return c.now }
	limiter.sleep = func(d time.Duration) {
		c.slept = append(c.slept, d)
		if c.advance {
			c.now = c.now.Add(d)
		}
	}
	limiter.mu.Unlock()
	t.Cleanup(func() {
		limiter.mu.Lock()
		limiter.states = make(map[string]*credentialState)
		limiter.now = time.Now
		limiter.sleep = time.Sleep
		limiter.mu.Unlock()
	})
	return c
}

func pacingConfig(minInterval, maxInterval, cooldown string) config.PluginConfig {
	cfg := config.Get()
	cfg.MinTokenInterval = minInterval
	cfg.MaxTokenInterval = maxInterval
	cfg.SuspendCooldown = cooldown
	return cfg
}

func TestAdmitCredentialPacesSameCredential(t *testing.T) {
	clock := newFakeClock().install(t)
	cfg := pacingConfig("1s", "1s", "1h")

	if err := admitCredential("a.json", Credential{}, cfg); err != nil {
		t.Fatal(err)
	}
	if len(clock.slept) != 0 {
		t.Fatalf("first request waited %v", clock.slept)
	}
	if err := admitCredential("a.json", Credential{}, cfg); err != nil {
		t.Fatal(err)
	}
	// The gap carries jitter, so it lands inside the configured window rather
	// than on an exact value.
	if len(clock.slept) != 1 || clock.slept[0] <= 0 || clock.slept[0] > time.Second {
		t.Fatalf("slept %v, want one gap of at most one second", clock.slept)
	}
}

func TestAdmitCredentialIsolatesCredentials(t *testing.T) {
	clock := newFakeClock().install(t)
	cfg := pacingConfig("1s", "1s", "1h")

	if err := admitCredential("a.json", Credential{}, cfg); err != nil {
		t.Fatal(err)
	}
	if err := admitCredential("b.json", Credential{}, cfg); err != nil {
		t.Fatal(err)
	}
	if len(clock.slept) != 0 {
		t.Fatalf("independent credentials waited %v", clock.slept)
	}
}

func TestAdmitCredentialFailsOverInsteadOfQueueingForever(t *testing.T) {
	clock := newFakeClock().concurrent().install(t)
	cfg := pacingConfig("1s", "2s", "1h")

	// Concurrent requests reserve their slots before sleeping, so a burst
	// stacks up; one that would wait past max_token_interval is refused
	// instead of stalling CPA.
	var refused error
	for i := 0; i < 10; i++ {
		if err := admitCredential("a.json", Credential{}, cfg); err != nil {
			refused = err
		}
	}
	up, ok := refused.(*UpstreamError)
	if !ok || up.Kind != ErrRateLimited || up.Status != http.StatusTooManyRequests {
		t.Fatalf("burst was never refused: %v", refused)
	}

	// Refused requests must NOT push nextSlot out into the future; once the
	// active window clears, subsequent requests must succeed without artificial starvation.
	clock.advance = true
	clock.now = clock.now.Add(3 * time.Second)
	if err := admitCredential("a.json", Credential{}, cfg); err != nil {
		t.Fatalf("subsequent request was blocked by phantom slots: %v", err)
	}
}

func TestAdmitCredentialHonoursDisableCooling(t *testing.T) {
	clock := newFakeClock().install(t)
	cfg := pacingConfig("1s", "1s", "1h")
	cred := Credential{DisableCooling: true}

	for i := 0; i < 3; i++ {
		if err := admitCredential("a.json", cred, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if len(clock.slept) != 0 {
		t.Fatalf("slept %v with cooling disabled", clock.slept)
	}
}

func TestRecordUpstreamFailureParksMonthlyLimit(t *testing.T) {
	clock := newFakeClock().install(t)
	cfg := pacingConfig("1s", "2s", "1h")
	up := &UpstreamError{Status: 402, Kind: ErrQuotaExhausted, Body: "monthly limit"}

	recordUpstreamFailure("a.json", up, cfg)

	err := admitCredential("a.json", Credential{}, cfg)
	parked, ok := err.(*UpstreamError)
	if !ok || parked.Status != http.StatusPaymentRequired || parked.Kind != ErrQuotaExhausted {
		t.Fatalf("got %v", err)
	}

	// The park ends when the UTC day rolls over.
	clock.now = nextUTCMidnight(clock.now)
	if err := admitCredential("a.json", Credential{}, cfg); err != nil {
		t.Fatalf("still parked after the UTC day rolled over: %v", err)
	}
}

func TestRecordUpstreamFailureParksSuspension(t *testing.T) {
	for _, body := range []string{"account SUSPENDED", blockedAccountMessage} {
		t.Run(body, func(t *testing.T) {
			clock := newFakeClock().install(t)
			cfg := pacingConfig("1s", "2s", "30m")
			recordUpstreamFailure("a.json", &UpstreamError{Status: 403, Kind: ErrForbidden, Body: body}, cfg)

			err := admitCredential("a.json", Credential{}, cfg)
			parked, ok := err.(*UpstreamError)
			if !ok || parked.Status != http.StatusForbidden || parked.Kind != ErrForbidden {
				t.Fatalf("got %v", err)
			}
			clock.now = clock.now.Add(30 * time.Minute)
			if err := admitCredential("a.json", Credential{}, cfg); err != nil {
				t.Fatalf("still parked after the cooldown: %v", err)
			}
		})
	}
}

func TestRecordUpstreamFailureIgnoresOtherErrors(t *testing.T) {
	newFakeClock().install(t)
	cfg := pacingConfig("1s", "2s", "1h")
	for _, kind := range []ErrorKind{ErrTokenInvalid, ErrRateLimited, ErrBadRequest, ErrRetryable, ErrUnknown} {
		recordUpstreamFailure("a.json", &UpstreamError{Status: 500, Kind: kind, Body: "x"}, cfg)
	}
	if err := admitCredential("a.json", Credential{}, cfg); err != nil {
		t.Fatalf("unexpected park: %v", err)
	}
}

func TestRecordUpstreamFailureLeavesOtherCredentialsAlone(t *testing.T) {
	newFakeClock().install(t)
	cfg := pacingConfig("1s", "2s", "1h")
	recordUpstreamFailure("a.json", &UpstreamError{Status: 402, Kind: ErrQuotaExhausted}, cfg)
	if err := admitCredential("b.json", Credential{}, cfg); err != nil {
		t.Fatalf("unrelated credential parked: %v", err)
	}
}

func TestNextUTCMidnight(t *testing.T) {
	got := nextUTCMidnight(time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC))
	want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAdmitCredentialWithEmptyAuthID(t *testing.T) {
	clock := newFakeClock().install(t)
	cfg := pacingConfig("1s", "1s", "1h")
	for i := 0; i < 3; i++ {
		if err := admitCredential("", Credential{}, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if len(clock.slept) != 0 {
		t.Fatalf("anonymous requests were paced: %v", clock.slept)
	}
}

func TestParkedErrorMentionsRemainingWindow(t *testing.T) {
	err := parkedError(parkMonthlyLimit, ErrQuotaExhausted, 90*time.Minute)
	if msg := err.Error(); msg != fmt.Sprintf("kiro upstream HTTP 402: credential is out of rotation for another %v (%s)", 90*time.Minute, parkMonthlyLimit) {
		t.Fatalf("message %q", msg)
	}
}

func TestRecordUpstreamFailureWithInvalidConfigKeepsState(t *testing.T) {
	clock := newFakeClock().install(t)
	cfg := pacingConfig("1s", "2s", "1h")
	cfg.SuspendCooldown = "soon"
	recordUpstreamFailure("a.json", &UpstreamError{Status: 402, Kind: ErrQuotaExhausted}, cfg)
	if len(clock.slept) != 0 {
		t.Fatalf("slept %v", clock.slept)
	}
	if err := admitCredential("a.json", Credential{}, cfg); err != nil {
		t.Fatalf("unexpected park: %v", err)
	}
}
