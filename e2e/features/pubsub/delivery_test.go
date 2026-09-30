//go:build e2e_fleet

package pubsub

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestPubsub_sameNodeDelivery: a message published through a node's gateway
// reaches a subscriber on that gateway, byte for byte, in the envelope
// {data, timestamp, topic}.
func TestPubsub_sameNodeDelivery(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	node := f.State.Nodes[0]
	c := n.Client.PinTo(node.PublicIP)
	s := subscribe(t, c, node.PublicIP, "room", tenancy.Owner(n), nil)
	warmUp(t, c, tenancy.Owner(n), s, "room")
	payload := []byte{0x00, 0xff, 'h', 'i', 0x80}
	publish(t, c, tenancy.Owner(n), "room", payload).Expect(t, http.StatusOK)
	s.await(t, payload, deliveryBudget)
}

// TestPubsub_crossNodeDelivery: a subscriber on each node receives what is
// published through every other node's gateway.
func TestPubsub_crossNodeDelivery(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	nodes := tenancy.PerNode(t, f, n.Client)
	subs := make([]*sub, len(nodes))
	for i, nc := range nodes {
		subs[i] = subscribe(t, nc.Client, nc.Node.PublicIP, "fanout", tenancy.Owner(n), nil)
	}
	for i, pub := range nodes {
		for j, s := range subs {
			if i != j {
				warmUp(t, pub.Client, tenancy.Owner(n), s, "fanout")
			}
		}
		msg := []byte("from-" + pub.Node.Name)
		publish(t, pub.Client, tenancy.Owner(n), "fanout", msg).Expect(t, http.StatusOK)
		for _, s := range subs {
			s.await(t, msg, deliveryBudget)
		}
	}
}

// TestPubsub_eachMessageDeliveredOnce: publishing N messages delivers each
// exactly once to a subscriber, whether it is on the publishing node (local
// delivery and the GossipSub echo must not both arrive) or on another.
func TestPubsub_eachMessageDeliveredOnce(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	nodes := tenancy.PerNode(t, f, n.Client)
	local := subscribe(t, nodes[0].Client, nodes[0].Node.PublicIP, "once", tenancy.Owner(n), nil)
	remote := subscribe(t, nodes[1].Client, nodes[1].Node.PublicIP, "once", tenancy.Owner(n), nil)
	warmUp(t, nodes[0].Client, tenancy.Owner(n), local, "once")
	warmUp(t, nodes[0].Client, tenancy.Owner(n), remote, "once")
	const count = 20
	for i := range count {
		publish(t, nodes[0].Client, tenancy.Owner(n), "once", []byte(fmt.Sprintf("m-%02d", i))).Expect(t, http.StatusOK)
	}
	sentinel := []byte("sentinel")
	publish(t, nodes[0].Client, tenancy.Owner(n), "once", sentinel).Expect(t, http.StatusOK)
	for name, s := range map[string]*sub{"same node": local, "other node": remote} {
		seen := map[string]int{}
		for _, fr := range s.await(t, sentinel, deliveryBudget) {
			seen[string(fr.Data)]++
		}
		for i := range count {
			if k := fmt.Sprintf("m-%02d", i); seen[k] != 1 {
				t.Errorf("%s subscriber received %s %d times, want once", name, k, seen[k])
			}
		}
	}
}

// largeData is the largest round payload whose publish body fits 1 MiB.
const largeData = 780_000

// TestPubsub_largeMessage: a message just under the 1 MiB publish limit
// (handlers/pubsub/publish_handler.go) arrives intact on the same node and on
// another; one over it is refused.
func TestPubsub_largeMessage(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	nodes := tenancy.PerNode(t, f, n.Client)
	local := subscribe(t, nodes[0].Client, nodes[0].Node.PublicIP, "big", tenancy.Owner(n), nil)
	remote := subscribe(t, nodes[1].Client, nodes[1].Node.PublicIP, "big", tenancy.Owner(n), nil)
	warmUp(t, nodes[0].Client, tenancy.Owner(n), remote, "big")
	local.drain(t)
	// The 1 MiB limit applies to the JSON body, where the data is base64:
	// 780,000 bytes of data is 1,040,000 of base64, just under 1 MiB.
	big := bytes.Repeat([]byte("0123456789"), largeData/10)
	publish(t, nodes[0].Client, tenancy.Owner(n), "big", big).Expect(t, http.StatusOK)
	local.await(t, big, deliveryBudget)
	remote.await(t, big, deliveryBudget)
	over := bytes.Repeat([]byte("x"), 1<<20)
	if resp := publish(t, nodes[0].Client, tenancy.Owner(n), "big", over); resp.Status != http.StatusBadRequest {
		t.Fatalf("a publish over the 1 MiB body limit answered %d", resp.Status)
	}
}

// TestPubsub_publishBatch: every message of a batch is delivered; a batch is
// validated before anything is delivered (publish_handler.go
// PublishBatchHandler).
func TestPubsub_publishBatch(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	node := f.State.Nodes[0]
	c := n.Client.PinTo(node.PublicIP)
	a := subscribe(t, c, node.PublicIP, "batch-a", tenancy.Owner(n), nil)
	b := subscribe(t, c, node.PublicIP, "batch-b", tenancy.Owner(n), nil)
	warmUp(t, c, tenancy.Owner(n), a, "batch-a")
	warmUp(t, c, tenancy.Owner(n), b, "batch-b")
	tenancy.Post(t, c, pathBatch, tenancy.Owner(n), batch("batch-a", "a1", "batch-b", "b1", "batch-a", "a2")).Expect(t, http.StatusOK)
	a.await(t, []byte("a2"), deliveryBudget)
	b.await(t, []byte("b1"), deliveryBudget)
	// A bad entry anywhere refuses the whole batch: the good first entry is not delivered.
	bad := map[string]any{"messages": []map[string]string{
		{"topic": "batch-a", "data_base64": b64("never")}, {"topic": "batch-a", "data_base64": "%%%"}}}
	tenancy.Post(t, c, pathBatch, tenancy.Owner(n), bad).Expect(t, http.StatusBadRequest)
	publish(t, c, tenancy.Owner(n), "batch-a", []byte("after-bad")).Expect(t, http.StatusOK)
	for _, fr := range a.await(t, []byte("after-bad"), deliveryBudget) {
		if string(fr.Data) == "never" {
			t.Fatal("an entry of a refused batch was delivered")
		}
	}
}

func TestPubsub_publishBatchLimits(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	var tooMany []string
	for i := range 101 { // MaxBatchSize is 100 (core/pkg/pubsub/publish.go)
		tooMany = append(tooMany, "t", fmt.Sprint(i))
	}
	cases := map[string]any{
		"empty":          map[string]any{"messages": []any{}},
		"over 100":       batch(tooMany...),
		"missing topic":  map[string]any{"messages": []map[string]string{{"data_base64": b64("x")}}},
		"entry over 1MB": map[string]any{"messages": []map[string]string{{"topic": "t", "data_base64": b64(string(bytes.Repeat([]byte("x"), 1<<20+1)))}}},
		"not json":       []byte("messages"),
	}
	for name, body := range cases {
		if resp := tenancy.Post(t, n.Client, pathBatch, tenancy.Owner(n), body); resp.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d: %.200s", name, resp.Status, resp.Body)
		}
	}
	tenancy.Post(t, n.Client, pathBatch, tenancy.Owner(n), batch(func() []string {
		var ok []string
		for i := range 100 {
			ok = append(ok, "t", fmt.Sprint(i))
		}
		return ok
	}()...)).Expect(t, http.StatusOK)
}

// batch builds a publish-batch body from topic, data pairs.
func batch(pairs ...string) map[string]any {
	var msgs []map[string]string
	for i := 0; i+1 < len(pairs); i += 2 {
		msgs = append(msgs, map[string]string{"topic": pairs[i], "data_base64": b64(pairs[i+1])})
	}
	return map[string]any{"messages": msgs}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
