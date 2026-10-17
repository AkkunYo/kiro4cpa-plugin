package kiro

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type LoginSession struct {
	State        string
	ExpiresAt    time.Time
	DeviceCode   string
	UserCode     string
	VerifyURL    string
	ClientID     string
	ClientSecret string
	Region       string
	StartURL     string
	Polling      bool
	Completed    *Credential
	Message      string
	Error        string
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

func getLoginSession(state string) (*LoginSession, bool) {
	loginSessionsMu.RLock()
	defer loginSessionsMu.RUnlock()
	s, ok := loginSessions[state]
	if !ok || time.Now().UTC().After(s.ExpiresAt) {
		return nil, false
	}
	return s, true
}

func completeLoginSession(state string, cred *Credential, message string) bool {
	loginSessionsMu.Lock()
	defer loginSessionsMu.Unlock()
	s, ok := loginSessions[state]
	if !ok || time.Now().UTC().After(s.ExpiresAt) {
		return false
	}
	s.Completed = cred
	s.Message = message
	s.Error = ""
	return true
}

func failLoginSession(state string, errMessage string) bool {
	loginSessionsMu.Lock()
	defer loginSessionsMu.Unlock()
	s, ok := loginSessions[state]
	if !ok || time.Now().UTC().After(s.ExpiresAt) {
		return false
	}
	s.Error = errMessage
	return true
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
