//go:build e2e_fleet

package referenceapps

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	// cronEveryMinute is a 6-field schedule (seconds first), as the
	// serverless feature uses; the relay function buckets fires by minute.
	cronEveryMinute = "0 * * * * *"
	cronSlots       = 2
	cronWindow      = 5 * time.Minute
	// publicURL is what the fetch function fetches: a public page every node
	// can reach, outside the fleet (http_fetch refuses internal addresses).
	publicURL   = "https://example.com/"
	ordersTopic = "ref-orders"
	// pathPushDevices registers a device (docs/whitepaper/technical-reference/appendices/i-api-surface.md "Push").
	pathPushDevices = "/v1/push/devices"
	pathPublish     = "/v1/pubsub/publish"
)

// TestReferenceFunctions_cronPubsubPushAndFetch: the WASM bundle deployed as
// four small functions. A publish on a topic fires the relay trigger once,
// which pushes to the user named in the message through the namespace's
// push and the push lands on every node's ntfy; the cron trigger fires every
// minute, once per minute across the namespace's gateways; http_fetch
// reaches a public page (website/src/docs/developer/functions.mdx "PubSub Triggers", "HTTP",
// website/src/docs/developer/push-notifications.mdx).
func TestReferenceFunctions_cronPubsubPushAndFetch(t *testing.T) {
	t.Parallel()
	realistic.RequireTinyGo(t)
	tn := realistic.NewTenant(t)
	for name, role := range map[string]string{"ref-cron": "cron", "ref-relay": "relay", "ref-fetch": "fetch", "ref-store": "store"} {
		tn.DeployFunction(t, name, role, false)
	}
	tn.N.CLI.MustOK(t, "function", "triggers", "add", "ref-cron", "--schedule", cronEveryMinute)
	tn.N.CLI.MustOK(t, "function", "triggers", "add", "ref-relay", "--topic", ordersTopic)
	usr := realistic.NewUsers(t, tn, roleRuntime, 1)[0]
	topic := randomTopic(t)
	registerNtfy(t, tn.C, usr, topic)
	marker := "order-" + randomTopic(t)
	publishJSON(t, tn.C, usr.Token(), ordersTopic, map[string]string{"user": usr.Subject(), "marker": marker})
	waitNtfy(t, tn, topic, marker)
	if n := fires(t, tn, usr.Token(), "relay", marker); n != 1 {
		t.Errorf("the relay trigger fired %d times for one publish, want once", n)
	}
	checkCron(t, tn, usr.Token())
	t.Run("http_fetch reaches a public page", func(t *testing.T) {
		realistic.RequireReachableFromNode(t, tn.F, tn.F.State.Nodes[0], publicURL)
		out, err := realistic.Invoke(t.Context(), tn.C, "ref-fetch", usr.Token(), map[string]string{"url": publicURL})
		if err != nil {
			t.Fatal(err)
		}
		status, _ := out["status"].(float64)
		if size, _ := out["bytes"].(float64); status != http.StatusOK || size == 0 {
			t.Errorf("http_fetch of %s: %v", publicURL, out)
		}
	})
}

func randomTopic(t testing.TB) string {
	t.Helper()
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "e2eref" + hex.EncodeToString(b)
}

// registerNtfy registers the user's device for push through the platform's
// ntfy under topic (website/src/docs/developer/push-notifications.mdx "Step 2").
func registerNtfy(t testing.TB, c *gw.Client, usr *realistic.User, topic string) {
	t.Helper()
	body := map[string]string{"device_id": "phone-" + topic[:12], "provider": "ntfy", "token": topic, "platform": "android"}
	if _, err := postJSON(t.Context(), c, pathPushDevices, usr.Token(), body, nil); err != nil {
		t.Fatalf("registering an ntfy device: %v", err)
	}
}

// publishJSON publishes v on topic as bearer.
func publishJSON(t testing.TB, c *gw.Client, bearer, topic string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]string{"topic": topic, "data_base64": base64.StdEncoding.EncodeToString(raw)}
	if _, err := postJSON(t.Context(), c, pathPublish, bearer, body, nil); err != nil {
		t.Fatalf("publishing on %s: %v", topic, err)
	}
}

// waitNtfy polls every node's ntfy for marker on topic: the instances share
// nothing and the gateway fans a push out to all of them.
func waitNtfy(t testing.TB, tn *realistic.Tenant, topic, marker string) {
	t.Helper()
	c := harness.GW(t).WithBase("https://push." + tn.F.State.BaseDomain)
	for _, n := range tn.F.State.Nodes {
		pinned := c.PinTo(n.PublicIP)
		eventually.Require(t, pollEvery, deliverBudget, "the push on "+n.Name+"'s ntfy", func() (bool, error) {
			r, err := pinned.Send(t.Context(), gw.Req{Path: "/" + topic + "/json", Query: map[string][]string{"poll": {"1"}}})
			if err != nil {
				return false, err
			}
			if r.Status != http.StatusOK {
				return false, fmt.Errorf("ntfy answered %d", r.Status)
			}
			return strings.Contains(string(r.Body), marker), nil
		})
	}
}

// fires counts the recorded fires of kind carrying marker.
func fires(t testing.TB, tn *realistic.Tenant, bearer, kind, marker string) int {
	t.Helper()
	out, err := realistic.Invoke(t.Context(), tn.C, "ref-store", bearer, map[string]string{"op": "fires", "kind": kind})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, row := range realistic.Rows(out) {
		if row["marker"] == marker {
			n, _ := row["n"].(float64)
			total += int(n)
		}
	}
	return total
}

// checkCron waits for cronSlots minutes with a cron fire and fails at once
// on a minute that fired twice.
func checkCron(t testing.TB, tn *realistic.Tenant, bearer string) {
	t.Helper()
	eventually.Require(t, 15*time.Second, cronWindow, fmt.Sprintf("%d cron minutes", cronSlots), func() (bool, error) {
		out, err := realistic.Invoke(t.Context(), tn.C, "ref-store", bearer, map[string]string{"op": "fires", "kind": "cron"})
		if err != nil {
			return false, err
		}
		rows := realistic.Rows(out)
		for _, r := range rows {
			if n, _ := r["n"].(float64); n > 1 {
				return false, eventually.Stop(fmt.Errorf("minute %v fired %v times", r["slot"], n))
			}
		}
		if len(rows) >= cronSlots {
			return true, nil
		}
		return false, fmt.Errorf("%d minutes so far", len(rows))
	})
}
