//go:build e2e_fleet

package serverless

import (
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

const (
	pathPublish = "/v1/pubsub/publish"
	// cronSchedule fires every two minutes (6-field, seconds first); the
	// fixture buckets fires by the same two minutes.
	cronSchedule = "0 */2 * * * *"
	cronWindow   = 9 * time.Minute
	minSlots     = 3
	maxDepth     = 5 // website/src/docs/developer/functions.mdx#depth-limiting
	triggerWait  = 3 * time.Minute
	quietWindow  = 45 * time.Second
)

type fireRow struct {
	Slot  string `json:"slot"`
	N     int    `json:"n"`
	Depth int    `json:"depth"`
}

// fires reads the fixture's e2e_fires table through a normal-mode function.
func fires(t *testing.T, fx *fixture, reader, sql string) []fireRow {
	t.Helper()
	res := call(t, fx, reader, map[string]any{"op": "db_query", "sql": sql})
	raw, _ := res["rows"].([]any)
	out := make([]fireRow, 0, len(raw))
	for _, r := range raw {
		m, _ := r.(map[string]any)
		n, _ := m["n"].(float64)
		d, _ := m["depth"].(float64)
		s, _ := m["slot"].(string)
		out = append(out, fireRow{Slot: s, N: int(n), Depth: int(d)})
	}
	return out
}

// TestTrigger_cronFiresOncePerSlot: every gateway of the namespace runs the
// cron scheduler, and the next_run_at compare-and-swap makes one of them
// fire each scheduled run (core/pkg/serverless/triggers/cron_scheduler.go).
// Over nine minutes an every-two-minutes schedule fires at least three slots
// and no slot twice.
func TestTrigger_cronFiresOncePerSlot(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-cron", yaml: "env:\n  E2E_MODE: cron\n"})
	deploy(t, fx, fnSpec{name: "e2e-cron-reader"})
	fx.n.CLI.MustOK(t, "function", "triggers", "add", "e2e-cron", "--schedule", cronSchedule)
	const q = "SELECT slot, COUNT(*) AS n FROM e2e_fires WHERE kind = 'cron' GROUP BY slot"
	eventually.Require(t, 30*time.Second, cronWindow+time.Minute, fmt.Sprintf("%d cron slots", minSlots), func() (bool, error) {
		rows := fires(t, fx, "e2e-cron-reader", q)
		for _, r := range rows {
			if r.N > 1 {
				return false, eventually.Stop(fmt.Errorf("slot %s fired %d times", r.Slot, r.N))
			}
		}
		if len(rows) >= minSlots {
			return true, nil
		}
		return false, fmt.Errorf("%d slots so far", len(rows))
	})
}

// TestTrigger_pubsubDepthLimited: a publish fires the trigger once
// (cross-node dedup), the function's own publish on the same topic fires it
// again one level deeper, and it stops at depth 5.
func TestTrigger_pubsubDepthLimited(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-loop", yaml: "env:\n  E2E_MODE: pubsub\n"})
	deploy(t, fx, fnSpec{name: "e2e-loop-reader"})
	topic := "e2e:loop:" + fx.n.Name
	fx.n.CLI.MustOK(t, "function", "triggers", "add", "e2e-loop", "--topic", topic)
	data := base64.StdEncoding.EncodeToString([]byte(`{"start":true}`))
	eventually.Require(t, 10*time.Second, triggerWait, "the first fire", func() (bool, error) {
		if tenancy.Post(t, fx.c, pathPublish, tenancy.Cred{Bearer: fx.admin}, map[string]string{"topic": topic, "data_base64": data}).Status != 200 {
			return false, errors.New("publish refused")
		}
		return len(depths(t, fx)) > 0, nil
	})
	eventually.Require(t, pollEvery, triggerWait, "depth 5 reached", func() (bool, error) {
		d := depths(t, fx)
		if d[maxDepth] > 0 {
			return true, nil
		}
		return false, fmt.Errorf("depths %v", d)
	})
	err := eventually.Poll(t.Context(), pollEvery, quietWindow, "a fire beyond depth 5", func() (bool, error) {
		for depth := range depths(t, fx) {
			if depth > maxDepth {
				return true, nil
			}
		}
		return false, nil
	})
	var timeout *eventually.TimeoutError
	if !errors.As(err, &timeout) {
		t.Errorf("a trigger fired beyond depth %d (%v): %v", maxDepth, err, depths(t, fx))
	}
}

// depths counts pubsub fires by trigger depth.
func depths(t *testing.T, fx *fixture) map[int]int {
	t.Helper()
	out := map[int]int{}
	for _, r := range fires(t, fx, "e2e-loop-reader", "SELECT depth, COUNT(*) AS n FROM e2e_fires WHERE kind = 'pubsub' GROUP BY depth") {
		out[r.Depth] = r.N
	}
	return out
}
