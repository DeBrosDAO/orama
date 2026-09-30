//go:build e2e_fleet

package pubsub

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

// The app pubsub API is a unix socket, 0600 in a 0700 runtime directory, and
// the service admits a connection only when SO_PEERCRED says the peer runs as
// its own user, the gateways' (docs/SECURITY.md; core/pkg/pubsub/socket.go).
const (
	pubsubDir    = "/run/orama-pubsub"
	pubsubSocket = pubsubDir + "/pubsub.sock"
	serviceUser  = "orama"
	// otherUser is an unprivileged account every Ubuntu image has.
	otherUser = "nobody"
)

// curlSocket asks the socket for /health and prints only the HTTP status
// (000 when the connection is refused or closed).
func curlSocket(asUser string) string {
	cmd := "curl -s --max-time 5 -o /dev/null -w '%{http_code}' --unix-socket " + pubsubSocket + " http://pubsub/health"
	if asUser == "" {
		return cmd
	}
	return "runuser -u " + asUser + " -- " + cmd
}

func TestPubsubSocket_modesAndOwner(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		for path, want := range map[string]string{pubsubDir: "700 " + serviceUser, pubsubSocket: "600 " + serviceUser} {
			out := f.MustExec(t, n, "stat -c '%a %U' "+path)
			if got := strings.TrimSpace(out.Stdout); got != want {
				t.Errorf("%s: %s is %q, want %q", n.Name, path, got, want)
			}
		}
	}
}

// TestPubsubSocket_onlyTheServiceUserIsServed: the gateways' user gets an
// answer; another user cannot connect, and root, which the file mode does not
// stop, is refused by the peer-credential check.
func TestPubsubSocket_onlyTheServiceUserIsServed(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		f.MustExec(t, n, "command -v curl && command -v runuser")
		if code := strings.TrimSpace(f.Exec(t, n, curlSocket(serviceUser)).Stdout); code != "200" {
			t.Errorf("%s: the %s user got HTTP %s from the pubsub socket, want 200", n.Name, serviceUser, code)
		}
		for _, who := range []string{otherUser, ""} {
			out := f.Exec(t, n, curlSocket(who))
			if code := strings.TrimSpace(out.Stdout); strings.HasPrefix(code, "2") {
				t.Errorf("%s: %q was served by the pubsub socket (HTTP %s)", n.Name, who, code)
			}
		}
	}
}
