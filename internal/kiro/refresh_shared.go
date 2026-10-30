package kiro

import (
	"errors"
	"sync"
)

// refreshes makes a credential spend each refresh token once. Kiro may rotate
// the refresh token, so two refreshes from the same stored state would leave
// the slower one holding a token Kiro has already replaced; the next
// scheduled refresh would then fail with invalid_grant and disable the account.
var refreshes = struct {
	mu       sync.Mutex
	inflight map[refreshKey]*refreshCall
	// latest is the last successful refresh per credential. A request that
	// still carries the replaced tokens gets the replacement instead of
	// spending the old refresh token again.
	latest map[string]refreshResult
}{
	inflight: map[refreshKey]*refreshCall{},
	latest:   map[string]refreshResult{},
}

type refreshKey struct {
	authID, accessToken, refreshToken string
}

type refreshCall struct {
	done chan struct{}
	cred Credential
	err  error
}

type refreshResult struct {
	from refreshKey
	to   Credential
}

// errRefreshAborted is what waiters see when the refresh they joined panicked.
var errRefreshAborted = errors.New("kiro refresh aborted")

func keyOf(authID string, c Credential) refreshKey {
	return refreshKey{authID: authID, accessToken: c.AccessToken, refreshToken: c.RefreshToken}
}

// sharedRefresh runs refresh at most once for one credential state. Callers
// that arrive while it runs wait for its result; callers that arrive later
// with the tokens it replaced get the credential it produced. Only the caller
// that runs refresh sees its side effects, such as persisting the result.
func sharedRefresh(authID string, c Credential, refresh func(Credential) (Credential, error)) (Credential, error) {
	// API key credentials never change on refresh, and without an auth ID
	// there is nothing to tell two credentials apart.
	if authID == "" || c.AuthMethod == AuthMethodAPIKey {
		return refresh(c)
	}
	key := keyOf(authID, c)

	refreshes.mu.Lock()
	if last, ok := refreshes.latest[authID]; ok && last.from == key {
		refreshes.mu.Unlock()
		return last.to, nil
	}
	if call, ok := refreshes.inflight[key]; ok {
		refreshes.mu.Unlock()
		<-call.done
		return call.cred, call.err
	}
	call := &refreshCall{done: make(chan struct{}), err: errRefreshAborted}
	refreshes.inflight[key] = call
	refreshes.mu.Unlock()

	defer func() {
		refreshes.mu.Lock()
		delete(refreshes.inflight, key)
		if call.err == nil {
			refreshes.latest[authID] = refreshResult{from: key, to: call.cred}
		}
		refreshes.mu.Unlock()
		close(call.done)
	}()
	call.cred, call.err = refresh(c)
	return call.cred, call.err
}
