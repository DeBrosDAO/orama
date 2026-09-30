//go:build e2e_fleet

package push

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	ntfyUnit = "orama-namespace-ntfy@index.service"
	ntfyPort = 10109
)

// pushHost is the self-hosted ntfy's public name (install sets
// ntfy_base_url to https://push.<base domain>).
func pushHost(f *fleet.Fleet) string { return "https://push." + f.State.BaseDomain }

// waitDelivered polls every node's ntfy cache for marker on topic: ntfy
// instances share nothing, and the gateway fans a publish out to all of them
// (docs/PUSH_NOTIFICATIONS.md#self-hosted-ntfy-installed-on-every-node).
func waitDelivered(t *testing.T, f *fleet.Fleet, topic, marker string) {
	t.Helper()
	c := harness.GW(t).WithBase(pushHost(f))
	for _, n := range f.State.Nodes {
		pinned := c.PinTo(n.PublicIP)
		eventually.Require(t, pollEvery, deliverBudget, "the push on "+n.Name+"'s ntfy", func() (bool, error) {
			r := pinned.MustSend(t, gw.Req{Path: "/" + topic + "/json", Query: map[string][]string{"poll": {"1"}}})
			if r.Status != http.StatusOK {
				return false, fmt.Errorf("ntfy answered %d", r.Status)
			}
			return strings.Contains(string(r.Body), marker), nil
		})
	}
}

// TestNtfy_selfHostedDeliveryEveryNode: with no credentials stored the
// namespace uses the platform ntfy; a push to a user's ntfy device lands on
// every node's ntfy; a UnifiedPush endpoint on the push host works; one on
// another host is refused at send (docs/PUSH_NOTIFICATIONS.md#unifiedpush-android--grapheneos-no-google-play-services).
func TestNtfy_selfHostedDeliveryEveryNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	nss := tenancy.Namespaces(t, f, 2, ns.Options{})
	n, foreign := nss[0], nss[1]
	owner := tenancy.Owner(n)
	user := n.Owner.Session.Subject
	topic, endpointTopic := randomTopic(t), randomTopic(t)
	register(t, n.Client, owner, "bare", "ntfy", topic).Expect(t, http.StatusOK)
	register(t, n.Client, owner, "up", "ntfy", pushHost(f)+"/"+endpointTopic).Expect(t, http.StatusOK)
	marker := "e2e-push-" + randomTopic(t)
	tenancy.Post(t, n.Client, pathSend, owner, map[string]any{"user_id": user, "title": "e2e", "body": marker, "priority": "high"}).
		Expect(t, http.StatusOK)
	waitDelivered(t, f, topic, marker)
	waitDelivered(t, f, endpointTopic, marker)
	fo := tenancy.Owner(foreign)
	register(t, foreign.Client, fo, "evil", "ntfy", "https://ntfy.example.com/"+randomTopic(t)).Expect(t, http.StatusOK)
	status(t, "a UnifiedPush endpoint on another host", tenancy.Post(t, foreign.Client, pathSend, fo,
		map[string]any{"user_id": foreign.Owner.Session.Subject, "title": "x", "body": "y"}), http.StatusBadGateway)
}

// TestNtfy_topicWithSlashFails: an ntfy topic is one path segment; a token
// with "/" is a different URL, and the send reports the failure
// (docs/PUSH_NOTIFICATIONS.md#step-2--choose-an-ntfy-topic-mode-android--web-only).
func TestNtfy_topicWithSlashFails(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	owner := tenancy.Owner(n)
	register(t, n.Client, owner, "slash", "ntfy", "ns-"+n.Name+"/user").Expect(t, http.StatusOK)
	status(t, "send to a topic with a slash", tenancy.Post(t, n.Client, pathSend, owner,
		map[string]any{"user_id": n.Owner.Session.Subject, "title": "x", "body": "y"}), http.StatusBadGateway)
}

// TestSend_statusCodes: /v1/push/send needs user_id, a JSON body under
// 64 KiB, POST, and the namespace's push grant; a user with no devices is not
// an error; ntfy runs on every node on loopback only.
func TestSend_statusCodes(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	owner := tenancy.Owner(n)
	status(t, "no user_id", tenancy.Post(t, n.Client, pathSend, owner, map[string]any{"title": "x"}), http.StatusBadRequest)
	status(t, "not JSON", tenancy.Post(t, n.Client, pathSend, owner, []byte("user_id=x")), http.StatusBadRequest)
	status(t, "over 64 KiB", tenancy.Post(t, n.Client, pathSend, owner, map[string]any{"user_id": "u", "body": strings.Repeat("b", maxSendBody)}),
		http.StatusBadRequest, http.StatusRequestEntityTooLarge)
	status(t, "GET send", tenancy.Get(t, n.Client, pathSend, owner), http.StatusMethodNotAllowed)
	status(t, "a user with no devices", tenancy.Post(t, n.Client, pathSend, owner, map[string]any{"user_id": "0xnobody", "title": "x"}), http.StatusOK)
	reader := tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleReader).Token()}
	tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, pathSend, reader, map[string]any{"user_id": "u"}), http.StatusForbidden, tenancy.CodeScope)
	tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, pathSend, tenancy.Cred{}, map[string]any{"user_id": "u"}), http.StatusUnauthorized, tenancy.CodeMissing)
	for _, node := range f.State.Nodes {
		if s := f.Unit(t, node, ntfyUnit); s != "active" {
			t.Errorf("%s: %s is %q", node.Name, ntfyUnit, s)
		}
		loopback := false
		for _, l := range f.Listeners(t, node) {
			if l.Port != ntfyPort {
				continue
			}
			if l.Addr != "127.0.0.1" {
				t.Errorf("%s: ntfy listens on %s", node.Name, l.Addr)
			}
			loopback = loopback || l.Addr == "127.0.0.1"
		}
		if !loopback {
			t.Errorf("%s: nothing listens on 127.0.0.1:%d, where ntfy must", node.Name, ntfyPort)
		}
	}
}
