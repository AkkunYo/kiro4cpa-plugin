package kiro

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
)

// Reasons a credential is parked. They only pick the status the request is
// refused with, because pluginabi.Error carries no retry-after field and the
// status alone decides CPA's cooldown: 402 and 403 cool the credential for
// 30 minutes and fail over, while 429 starts a one-second ladder.
const (
	parkMonthlyLimit = "monthly limit reached"
	parkSuspended    = "account suspended"
)

const jitterPercent = 0.3

type credentialState struct {
	nextSlot time.Time
	parkedTo time.Time
	parkKind ErrorKind
	parkWhy  string
}

// rateLimiter paces requests per credential and remembers conditions Kiro
// reports that outlive a single request. Credential selection, failover and
// the 429 backoff ladder stay with CPA and are not duplicated here.
type rateLimiter struct {
	mu     sync.Mutex
	states map[string]*credentialState
	rng    *rand.Rand
	now    func() time.Time
	sleep  func(time.Duration)
}

var limiter = newRateLimiter()

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		states: make(map[string]*credentialState),
		// Only jitters request gaps; nothing security-relevant depends on it.
		rng:   rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec G404
		now:   time.Now,
		sleep: time.Sleep,
	}
}

func (rl *rateLimiter) state(authID string) *credentialState {
	st, ok := rl.states[authID]
	if !ok {
		st = &credentialState{}
		rl.states[authID] = st
	}
	return st
}

// interval draws the jittered gap between two requests of one credential. The
// result never exceeds MaxTokenInterval: that value is also the cap admitCredential
// refuses a wait at, so jitter above it would reject a request that is only
// one slot behind.
func (rl *rateLimiter) interval(p config.Pacing) time.Duration {
	gap := p.MinTokenInterval
	if spread := p.MaxTokenInterval - p.MinTokenInterval; spread > 0 {
		gap += time.Duration(rl.rng.Int63n(int64(spread)))
	}
	gap += time.Duration(float64(gap) * jitterPercent * (rl.rng.Float64()*2 - 1))
	if gap <= 0 {
		return 0
	}
	if gap > p.MaxTokenInterval {
		return p.MaxTokenInterval
	}
	return gap
}

// admitCredential refuses a parked credential before any upstream call, then
// paces the request to its slot. The ABI gives the executor no caller context,
// so the wait is capped: a burst that would queue past max_token_interval fails
// over instead of stalling CPA.
func admitCredential(authID string, cred Credential, cfg config.PluginConfig) error {
	if cred.DisableCooling || authID == "" {
		return nil
	}
	pacing, err := cfg.Durations()
	if err != nil {
		// The config was validated on load, so this only happens for a value
		// written after the fact; pacing is better skipped than failed.
		log.Printf("kiro4cpa: ignoring invalid pacing config: %v", err)
		return nil
	}

	rl := limiter
	rl.mu.Lock()
	st := rl.state(authID)
	now := rl.now()
	if now.Before(st.parkedTo) {
		reason, kind := st.parkWhy, st.parkKind
		remaining := st.parkedTo.Sub(now)
		rl.mu.Unlock()
		return parkedError(reason, kind, remaining)
	}
	slot := st.nextSlot
	if slot.Before(now) {
		slot = now
	}
	st.nextSlot = slot.Add(rl.interval(pacing))
	rl.mu.Unlock()

	wait := slot.Sub(now)
	if wait <= 0 {
		return nil
	}
	if wait > pacing.MaxTokenInterval {
		return &UpstreamError{
			Status: http.StatusTooManyRequests,
			Kind:   ErrRateLimited,
			Body:   fmt.Sprintf("credential %s is paced for another %v", authID, wait),
		}
	}
	rl.sleep(wait)
	return nil
}

// recordUpstreamFailure remembers a 402 until the next UTC day and a suspended
// account for the configured cooldown, so later requests skip the credential
// without paying for an upstream round trip.
func recordUpstreamFailure(authID string, err error, cfg config.PluginConfig) {
	up, ok := err.(*UpstreamError)
	if !ok || authID == "" {
		return
	}
	pacing, errPacing := cfg.Durations()
	if errPacing != nil {
		return
	}
	var kind ErrorKind
	var why string
	switch {
	case up.Kind == ErrQuotaExhausted:
		kind, why = ErrQuotaExhausted, parkMonthlyLimit
	case up.Kind == ErrForbidden && isSuspendedBody(up.Body):
		kind, why = ErrForbidden, parkSuspended
	default:
		return
	}
	limiter.mu.Lock()
	now := limiter.now()
	var until time.Time
	if kind == ErrQuotaExhausted {
		// Kiro meters monthly quota per UTC day, so the credential is retried
		// when the day rolls over rather than through CPA's seconds-long ladder.
		until = nextUTCMidnight(now)
	} else {
		until = now.Add(pacing.SuspendCooldown)
	}
	st := limiter.state(authID)
	st.parkedTo, st.parkKind, st.parkWhy = until, kind, why
	limiter.mu.Unlock()
	log.Printf("kiro4cpa: credential %s parked for %v (%s)", authID, until.Sub(now).Round(time.Second), why)
}

// parkedError reports a parked credential with the status CPA needs to fail
// over at once instead of retrying the same one.
func parkedError(reason string, kind ErrorKind, remaining time.Duration) *UpstreamError {
	status := http.StatusTooManyRequests
	if kind == ErrQuotaExhausted {
		status = http.StatusPaymentRequired
	} else if kind == ErrForbidden {
		status = http.StatusForbidden
	}
	return &UpstreamError{
		Status: status,
		Kind:   kind,
		Body:   fmt.Sprintf("credential is out of rotation for another %v (%s)", remaining.Round(time.Second), reason),
	}
}

// isSuspendedBody reports the markers Kiro uses for a suspended account.
func isSuspendedBody(body string) bool {
	return strings.Contains(body, "SUSPENDED") || strings.Contains(body, blockedAccountMessage)
}

// nextUTCMidnight is the window applied after a monthly-limit response.
func nextUTCMidnight(now time.Time) time.Time {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day()+1, 0, 0, 0, 0, time.UTC)
}
