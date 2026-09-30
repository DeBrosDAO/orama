package gw

import (
	"context"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// LobbyNamespace is where a challenge with no namespace signs in: it belongs to
// nobody, and its session reaches only POST /v1/namespaces.
const LobbyNamespace = "default"

// logoutBudget bounds the logout a user's cleanup performs.
const logoutBudget = 30 * time.Second

// ForFleet returns the client for the run's public gateway, attributed to t.
func ForFleet(t testing.TB, f *fleet.Fleet) *Client {
	t.Helper()
	c, err := New(f.State.GatewayURL, f.State.CAFile, f.Recorder())
	if err != nil {
		t.Fatalf("failed to build the gateway client: %v", err)
	}
	return c.For(t)
}

// NamespaceURL is a namespace's own gateway, https://ns-<name>.<base domain>.
func NamespaceURL(st *fleet.State, namespace string) string {
	return "https://ns-" + namespace + "." + st.BaseDomain
}

// NamespacePinned is c aimed at namespace's own gateway and pinned to one
// node's public address ip (SNI and Host stay ns-<name>.<base domain>).
func (c *Client) NamespacePinned(st *fleet.State, namespace, ip string) *Client {
	return c.WithBase(NamespaceURL(st, namespace)).PinTo(ip)
}

// User is a signed-in throwaway wallet.
type User struct {
	Wallet    *wallet.EVM
	Device    *wallet.Device
	Session   *Session
	Namespace string
	// Client is the public gateway client, attributed to the test.
	Client *Client
}

// UserOption configures NewUser.
type UserOption func(*userConfig)

type userConfig struct {
	deviceAlg string
	unpaced   bool
}

// WithDevice binds the session to a fresh device key (wallet.AlgEd25519 or wallet.AlgES256).
func WithDevice(alg string) UserOption {
	return func(c *userConfig) { c.deviceAlg = alg }
}

// Unpaced makes the user's Client unpaced (see (*Client).Unpaced) once it has
// signed in; the sign-in itself stays paced. ONLY for rate-limiter tests.
func Unpaced() UserOption {
	return func(c *userConfig) { c.unpaced = true }
}

// NewUser creates a wallet and signs it in to namespace ("" or LobbyNamespace
// for the lobby). The namespace must already admit the wallet. A cleanup logs
// every session of the wallet out.
func NewUser(t testing.TB, f *fleet.Fleet, namespace string, opts ...UserOption) *User {
	t.Helper()
	var cfg userConfig
	for _, o := range opts {
		o(&cfg)
	}
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	u := &User{Wallet: w, Namespace: namespace, Client: ForFleet(t, f)}
	if cfg.deviceAlg != "" {
		u.Device = NewDevice(t, cfg.deviceAlg)
	}
	chNamespace := namespace
	if chNamespace == LobbyNamespace {
		chNamespace = ""
	}
	u.Session, err = u.Client.SignIn(t.Context(), w, chNamespace, u.Device)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.unpaced {
		u.Client = u.Client.Unpaced()
	}
	t.Cleanup(func() { u.logoutAll(t) })
	return u
}

// NewDevice generates a device key of alg (wallet.AlgEd25519 or wallet.AlgES256).
func NewDevice(t testing.TB, alg string) *wallet.Device {
	t.Helper()
	var d *wallet.Device
	var err error
	switch alg {
	case wallet.AlgEd25519:
		d, err = wallet.NewEd25519Device()
	case wallet.AlgES256:
		d, err = wallet.NewES256Device()
	default:
		t.Fatalf("unknown device algorithm %q", alg)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (u *User) logoutAll(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), logoutBudget)
	defer cancel()
	resp, err := u.Client.Logout(ctx, u.Session.AccessToken, u.Session.RefreshToken, u.Session.Namespace, true)
	if err != nil && (resp == nil || resp.Status != 401) {
		// 401 means a test already ended the session, which is not a leak.
		t.Errorf("cleanup: failed to log %s out: %v", u.Wallet.Address(), err)
	}
}

// Token is the current access token.
func (u *User) Token() string { return u.Session.AccessToken }
