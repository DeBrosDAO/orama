package auth

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Renewing a session that lives on disk.
//
// A refresh token rotates on use: the gateway retires the one presented, and
// presenting it again is refused as a replay. Every command loads the
// credential file before it asks for a bearer, and `orama monitor` loads it on
// every renewal for hours, so two renewals racing — two goroutines, or a
// monitor in one terminal and a command in another — both present the token
// they loaded. The first rotates it; the second is refused and, before this,
// ended the session.
//
// So the decision to refresh is made on what is on disk now, not on what the
// caller loaded: under sessionMu (this process) and the credential file's lock
// (every process), the stored session is read again, a renewal somebody else
// already made is used as it is, and a new one is written back before the lock
// is released.

// sessionState is the part of a credential a renewal changes.
type sessionState struct {
	accessToken  string
	refreshToken string
	expiresAt    time.Time
}

func sessionOf(creds *Credentials) sessionState {
	return sessionState{creds.AccessToken, creds.RefreshToken, creds.AccessTokenExpiresAt}
}

func (s sessionState) applyTo(creds *Credentials) {
	creds.AccessToken = s.accessToken
	creds.RefreshToken = s.refreshToken
	creds.AccessTokenExpiresAt = s.expiresAt
}

func (s sessionState) equal(other sessionState) bool {
	return s.accessToken == other.accessToken &&
		s.refreshToken == other.refreshToken &&
		s.expiresAt.Equal(other.expiresAt)
}

// renewStoredSession renews creds against the session on disk. The caller
// holds sessionMu.
//
// The stored session wins over the caller's copy: it is the newest there is,
// because every renewal writes it back before releasing the lock. What the
// renewal changes — a rotated token, or a refresh token the gateway refused —
// is written back whether or not the renewal as a whole succeeded.
func renewStoredSession(gatewayURL string, creds *Credentials) (string, error) {
	unlock, err := lockCredentialFile()
	if err != nil {
		return "", err
	}
	defer unlock()

	// loadEnhancedStore, not LoadEnhancedCredentials: the lock is already held,
	// and a migration save would take it a second time.
	disk, _, err := loadEnhancedStore()
	if err != nil {
		return "", fmt.Errorf("reload the stored session before renewing it: %w", err)
	}
	stored := findStoredCredential(disk, gatewayURL, creds)
	if stored == nil {
		// The credential was signed out of while this renewal waited.
		// Renewing it in memory would keep a signed-out session alive in
		// a long-running command; writing it back would sign it in again.
		return "", fmt.Errorf("the credential for %s was signed out; run 'orama auth login'", gatewayURL)
	}
	sessionOf(stored).applyTo(creds)
	if creds.HasLiveAccessToken() {
		return creds.AccessToken, nil
	}

	before := sessionOf(creds)
	token, renewErr := renewSession(gatewayURL, creds)
	if !sessionOf(creds).equal(before) {
		sessionOf(creds).applyTo(stored)
		persistSession(disk)
	}
	return token, renewErr
}

// findStoredCredential is the stored counterpart of creds: the same wallet and
// namespace on the same gateway, which is how AddCredential tells credentials
// apart.
func findStoredCredential(store *EnhancedCredentialStore, gatewayURL string, creds *Credentials) *Credentials {
	gateway := store.Gateways[gatewayURL]
	if gateway == nil {
		return nil
	}
	for _, candidate := range gateway.Credentials {
		if candidate != nil && strings.EqualFold(candidate.Wallet, creds.Wallet) &&
			candidate.Namespace == creds.Namespace {
			return candidate
		}
	}
	return nil
}

// persistSession writes the rotated session back.
//
// A refresh token rotates on use, so a failure to save it means the next
// command presents one the gateway has already retired — and is told it is
// replaying a stolen credential. It is reported rather than returned because
// the caller has a working token in hand either way, and turning a saved-file
// problem into a failed command would be worse than the warning.
func persistSession(store *EnhancedCredentialStore) {
	if err := store.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: the renewed session could not be saved (%v); "+
			"the next command will have to sign in again\n", err)
	}
}
