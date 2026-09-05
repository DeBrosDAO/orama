package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The client half of the device flow. What matters here is that the outcomes
// the gateway names are acted on rather than reported: `slow_down` means back
// off, `access_denied` means stop, and only one of them means keep waiting.

// deviceGateway answers /v1/auth/device and /v1/auth/device/token with a
// scripted sequence of poll outcomes.
type deviceGateway struct {
	*httptest.Server
	outcomes []string
	polls    int
	interval int
}

func newDeviceGateway(t *testing.T, outcomes ...string) *deviceGateway {
	t.Helper()
	g := &deviceGateway{outcomes: outcomes, interval: 1}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/device":
			fmt.Fprintf(w, `{"device_code":"dev-secret","user_code":"BCDF-GHJK","expires_in":600,"interval":%d}`, g.interval)
		case "/v1/auth/device/token":
			if g.polls >= len(g.outcomes) {
				t.Errorf("the client polled %d times; the script has %d outcomes", g.polls+1, len(g.outcomes))
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			outcome := g.outcomes[g.polls]
			g.polls++
			if outcome == "ok" {
				fmt.Fprint(w, `{"access_token":"the-session","refresh_token":"the-refresh","expires_in":900,`+
					`"subject":"0xowner","namespace":"anchat"}`)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":%q}`, outcome)
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

// fastLogin is a pending login with a poll interval short enough for a test.
func fastLogin(t *testing.T, gateway *deviceGateway) *DeviceLogin {
	t.Helper()
	login, err := StartDeviceLogin(gateway.URL, "anchat")
	if err != nil {
		t.Fatalf("StartDeviceLogin: %v", err)
	}
	login.Interval = time.Millisecond
	return login
}

func TestStartDeviceLogin_returnsBothCodes(t *testing.T) {
	gateway := newDeviceGateway(t)

	login, err := StartDeviceLogin(gateway.URL, "anchat")
	if err != nil {
		t.Fatalf("StartDeviceLogin: %v", err)
	}
	if login.DeviceCode != "dev-secret" || login.UserCode != "BCDF-GHJK" {
		t.Fatalf("login = %+v", login)
	}
	if login.Interval != time.Second {
		t.Errorf("interval = %v, want the gateway's", login.Interval)
	}
	if time.Until(login.ExpiresAt) < 9*time.Minute {
		t.Errorf("expires at %v, which is not the ten minutes the gateway gave", login.ExpiresAt)
	}
}

// A gateway older than this CLI has no device endpoint. Saying "no device code"
// leaves somebody looking for the bug in their own shell.
func TestStartDeviceLogin_saysWhenTheGatewayCannotDoThis(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	_, err := StartDeviceLogin(server.URL, "anchat")
	if err == nil {
		t.Fatal("a gateway that answered with nothing produced a login")
	}
	if !strings.Contains(err.Error(), "older than this CLI") {
		t.Errorf("the error does not name the cause: %v", err)
	}
}

func TestPollDeviceLogin_waitsThenCollectsTheSession(t *testing.T) {
	gateway := newDeviceGateway(t, outcomePending, outcomePending, "ok")

	creds, err := PollDeviceLogin(gateway.URL, fastLogin(t, gateway), nil)
	if err != nil {
		t.Fatalf("PollDeviceLogin: %v", err)
	}
	if creds.AccessToken != "the-session" || creds.RefreshToken != "the-refresh" {
		t.Errorf("creds = %+v", creds)
	}
	if !creds.HasLiveAccessToken() {
		t.Error("the collected session is not live")
	}
	// A login hands back a session, not a credential to keep. Storing a key
	// here would rebuild the thing this flow exists to remove.
	if creds.APIKey != "" {
		t.Errorf("the login stored an API key: %q", creds.APIKey)
	}
	if creds.Wallet != "0xowner" || creds.Namespace != "anchat" {
		t.Errorf("creds = %+v", creds)
	}
}

// slow_down is the gateway saying the client is already too fast. Carrying on
// at the same rate would keep it too fast for the rest of the wait.
func TestPollDeviceLogin_backsOffWhenToldTo(t *testing.T) {
	gateway := newDeviceGateway(t, outcomeSlowDown, "ok")
	login := fastLogin(t, gateway)
	login.Interval = 20 * time.Millisecond

	started := time.Now()
	if _, err := PollDeviceLogin(gateway.URL, login, nil); err != nil {
		t.Fatalf("PollDeviceLogin: %v", err)
	}

	// One poll at the interval, then one at twice it.
	if elapsed := time.Since(started); elapsed < 3*login.Interval {
		t.Errorf("the two polls took %v; the second did not back off", elapsed)
	}
}

func TestPollDeviceLogin_stopsOnARefusal(t *testing.T) {
	gateway := newDeviceGateway(t, outcomePending, outcomeDenied)

	_, err := PollDeviceLogin(gateway.URL, fastLogin(t, gateway), nil)
	if err == nil {
		t.Fatal("a refused login was collected")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("the error does not say it was refused: %v", err)
	}
	if gateway.polls != 2 {
		t.Errorf("%d polls; a refusal should stop the loop", gateway.polls)
	}
}

func TestPollDeviceLogin_stopsWhenTheCodeExpires(t *testing.T) {
	gateway := newDeviceGateway(t, outcomeExpired)

	_, err := PollDeviceLogin(gateway.URL, fastLogin(t, gateway), nil)
	if err == nil {
		t.Fatal("an expired login was collected")
	}
	if !strings.Contains(err.Error(), "orama auth login") {
		t.Errorf("the error does not say what to do: %v", err)
	}
}

// The deadline is the client's too: a gateway that keeps answering
// authorization_pending must not keep the CLI waiting for ever.
func TestPollDeviceLogin_givesUpAtItsOwnDeadline(t *testing.T) {
	gateway := newDeviceGateway(t)
	login := fastLogin(t, gateway)
	login.ExpiresAt = time.Now().Add(-time.Second)

	_, err := PollDeviceLogin(gateway.URL, login, nil)
	if err == nil {
		t.Fatal("polling continued past the deadline")
	}
	if gateway.polls != 0 {
		t.Errorf("%d polls after the deadline had already passed", gateway.polls)
	}
}

func TestPollDeviceLogin_reportsProgressOncePerPoll(t *testing.T) {
	gateway := newDeviceGateway(t, outcomePending, outcomePending, "ok")
	ticks := 0

	if _, err := PollDeviceLogin(gateway.URL, fastLogin(t, gateway), func() { ticks++ }); err != nil {
		t.Fatalf("PollDeviceLogin: %v", err)
	}
	if ticks != 3 {
		t.Errorf("%d progress ticks for 3 polls", ticks)
	}
}

func TestNamespaceGatewayURL(t *testing.T) {
	for _, tc := range []struct{ gateway, namespace, want string }{
		{"https://orama-devnet.network", "anchat", "https://ns-anchat.orama-devnet.network"},
		{"https://orama-devnet.network", "default", "https://orama-devnet.network"},
		{"https://orama-devnet.network", "", ""},
		{"", "anchat", ""},
	} {
		if got := namespaceGatewayURL(tc.gateway, tc.namespace); got != tc.want {
			t.Errorf("namespaceGatewayURL(%q, %q) = %q, want %q", tc.gateway, tc.namespace, got, tc.want)
		}
	}
}
