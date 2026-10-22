package kiro

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// LoginSession tracks one CPA login attempt from method selection to a saved
// credential. The connect page drives it through management routes.
type LoginSession struct {
	State     string
	ExpiresAt time.Time
	// Method is the login method the user picked; empty until they choose.
	Method string

	// Device flow (Builder ID / IdC).
	DeviceCode   string
	UserCode     string
	VerifyURL    string
	ClientID     string
	ClientSecret string
	Region       string
	StartURL     string
	Polling      bool

	// Social (Google / GitHub) PKCE flow.
	CodeVerifier string
	SocialState  string

	Completed *Credential
	Message   string
	Error     string
}

var (
	loginSessionsMu sync.RWMutex
	loginSessions   = make(map[string]*LoginSession)
)

func createLoginSession(ttl time.Duration) (*LoginSession, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	state := hex.EncodeToString(b)
	now := time.Now().UTC()
	session := &LoginSession{
		State:     state,
		ExpiresAt: now.Add(ttl),
	}

	loginSessionsMu.Lock()
	defer loginSessionsMu.Unlock()
	cleanupLoginSessionsLocked(now)
	loginSessions[state] = session
	return session, nil
}

// getLoginSession returns a copy, so callers never read fields that a
// background poller is writing under the lock.
func getLoginSession(state string) (*LoginSession, bool) {
	loginSessionsMu.RLock()
	defer loginSessionsMu.RUnlock()
	s, ok := loginSessions[state]
	if !ok || time.Now().UTC().After(s.ExpiresAt) {
		return nil, false
	}
	snapshot := *s
	return &snapshot, true
}

func completeLoginSession(state string, cred *Credential, message string) bool {
	return updateLoginSession(state, func(s *LoginSession) {
		s.Completed = cred
		s.Message = message
		s.Error = ""
		s.Polling = false
		s.CodeVerifier = ""
	})
}

func failLoginSession(state string, errMessage string) bool {
	return updateLoginSession(state, func(s *LoginSession) {
		s.Error = errMessage
		s.Polling = false
	})
}

func updateLoginSession(state string, fn func(s *LoginSession)) bool {
	loginSessionsMu.Lock()
	defer loginSessionsMu.Unlock()
	s, ok := loginSessions[state]
	if !ok || time.Now().UTC().After(s.ExpiresAt) {
		return false
	}
	fn(s)
	return true
}

func cleanupLoginSessions(now time.Time) {
	loginSessionsMu.Lock()
	defer loginSessionsMu.Unlock()
	cleanupLoginSessionsLocked(now)
}

func cleanupLoginSessionsLocked(now time.Time) {
	for k, v := range loginSessions {
		if now.After(v.ExpiresAt) {
			delete(loginSessions, k)
		}
	}
}
