package kiro

import (
	"testing"
	"time"
)

func TestLoginSessionLifecycle(t *testing.T) {
	session, err := createLoginSession(100 * time.Millisecond)
	if err != nil {
		t.Fatalf("createLoginSession failed: %v", err)
	}
	if len(session.State) < 16 {
		t.Fatalf("session state too short: %q", session.State)
	}

	got, ok := getLoginSession(session.State)
	if !ok || got.State != session.State {
		t.Fatalf("getLoginSession failed to find active session")
	}

	cred := &Credential{AuthMethod: "api_key", KiroAPIKey: "test-key"}
	if !completeLoginSession(session.State, cred, "connected") {
		t.Fatalf("completeLoginSession failed")
	}

	got, _ = getLoginSession(session.State)
	if got.Completed == nil || got.Completed.KiroAPIKey != "test-key" {
		t.Fatalf("expected completed credential to match")
	}

	time.Sleep(120 * time.Millisecond)
	cleanupLoginSessions(time.Now().UTC())
	if _, ok := getLoginSession(session.State); ok {
		t.Fatalf("expected session to be pruned after expiration")
	}
}
