//go:build e2e_fleet

package pubsub

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	// codeReservedKey is the answer to a payload carrying the reserved `_orama` key.
	codeReservedKey = "PUBSUB_RESERVED_KEY"
	// grantCacheBudget: a grant taken away reaches the data plane within the
	// gateway's ten-second grant cache (docs/AUTH.md), plus the round trip.
	grantCacheBudget = 25 * time.Second
)

// TestPublishGuard_walletWithNoGrantMaySubscribeButNotPublish: a signed-in
// wallet that holds no grant in the namespace is an application's end user. It
// keeps the data plane and may subscribe, but publishing is refused with
// INSUFFICIENT_SCOPE and a message naming the remedy, on publish and
// publish-batch alike (docs/AUTH.md, bugboard #733). The wallet gets there as
// an end user would: its session outlives its grant.
func TestPublishGuard_walletWithNoGrantMaySubscribeButNotPublish(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	m := tenancy.Member(t, n, tenancy.RoleRuntime)
	who := tenancy.Cred{Bearer: m.Token()}
	publish(t, n.Client, who, "t", []byte("while granted")).Expect(t, http.StatusOK)

	n.Client.MustSend(t, gw.Req{Method: http.MethodDelete, Path: tenancy.PathMembers + "/" + m.Wallet.Address(), Bearer: n.Owner.Token()}).Expect(t, http.StatusOK)

	eventually.Require(t, pollEvery, grantCacheBudget, "the removed grant to stop allowing publishing", func() (bool, error) {
		return publish(t, n.Client, who, "t", []byte("after")).Status == http.StatusForbidden, nil
	})
	refused := publish(t, n.Client, who, "t", []byte("after"))
	tenancy.ExpectRefused(t, refused, http.StatusForbidden, tenancy.CodeScope)
	for _, want := range []string{"pubsub write", "publish from a function"} {
		if !strings.Contains(string(refused.Body), want) {
			t.Errorf("the refusal does not name the remedy %q: %.300s", want, refused.Body)
		}
	}
	tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, pathBatch, who, batch("t", "x")), http.StatusForbidden, tenancy.CodeScope)
	subscribe(t, n.Client, "", "t", who, nil)
}

// TestPublishGuard_reservedKeyIsRefusedForEveryCaller: `_orama` marks events
// the platform itself publishes, so no publish route carries it, not even for
// the namespace owner; the batch is refused whole, naming the item, and
// ordinary payloads are unaffected (docs/SERVERLESS.md, bugboard #733).
func TestPublishGuard_reservedKeyIsRefusedForEveryCaller(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	owner := tenancy.Owner(n)

	r := publish(t, n.Client, owner, "t", []byte(`{"_orama":"ephemeral.clear","key":"k"}`))
	if r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), codeReservedKey) {
		t.Fatalf("a reserved payload on publish: HTTP %d %.300s, want 400 %s", r.Status, r.Body, codeReservedKey)
	}
	b := tenancy.Post(t, n.Client, pathBatch, owner, batch("t", `{"ok":1}`, "t", `{"_orama":"x"}`))
	if b.Status != http.StatusBadRequest || !strings.Contains(string(b.Body), codeReservedKey) || !strings.Contains(string(b.Body), "index 1") {
		t.Fatalf("a batch with one reserved item: HTTP %d %.300s, want 400 %s naming index 1", b.Status, b.Body, codeReservedKey)
	}
	publish(t, n.Client, owner, "t", []byte(`{"type":"message","body":"hi"}`)).Expect(t, http.StatusOK)
}

// codeReservedTopic is the answer to a publish to, or an ungranted subscribe
// to, a topic under the platform's `_orama/` prefix.
const codeReservedTopic = "PUBSUB_RESERVED_TOPIC"

// TestPublishGuard_reservedTopicIsThePlatforms: topics under `_orama/` (the
// WebRTC membership events) are the platform's. No credential, the owner's
// included, publishes to one; a signed-in user holding no grant may not
// subscribe to one; a grant that covers the topic may (docs/AUTH.md "Reserved
// pub/sub topics").
func TestPublishGuard_reservedTopicIsThePlatforms(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	owner := tenancy.Owner(n)
	const topic = "_orama/webrtc/e2e-room"

	r := publish(t, n.Client, owner, topic, []byte(`{"type":"forged.join"}`))
	if r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), codeReservedTopic) {
		t.Fatalf("the owner publishing to %s: HTTP %d %.300s, want 403 %s", topic, r.Status, r.Body, codeReservedTopic)
	}

	m := tenancy.Member(t, n, tenancy.RoleRuntime)
	who := tenancy.Cred{Bearer: m.Token()}
	subscribe(t, n.Client, "", topic, who, nil)

	n.Client.MustSend(t, gw.Req{Method: http.MethodDelete, Path: tenancy.PathMembers + "/" + m.Wallet.Address(), Bearer: n.Owner.Token()}).Expect(t, http.StatusOK)
	eventually.Require(t, pollEvery, grantCacheBudget, "an ungranted subscribe to a reserved topic to be refused", func() (bool, error) {
		return subscribeStatus(t, n.Client, topic, who) == http.StatusForbidden, nil
	})
}

// subscribeStatus opens a subscription to topic and reports the upgrade's HTTP
// status: 101 when it was accepted (the socket is closed again at once), the
// refusal's status otherwise.
func subscribeStatus(t testing.TB, c *gw.Client, topic string, who tenancy.Cred) int {
	t.Helper()
	q := url.Values{}
	q.Set("topic", topic)
	conn, resp, err := dial(t, c, "", pathWS+"?"+q.Encode(), who)
	if err == nil {
		_ = conn.Close()
		return http.StatusSwitchingProtocols
	}
	if resp == nil {
		t.Fatalf("subscribing to %q: no answer: %v", topic, err)
	}
	return resp.StatusCode
}
