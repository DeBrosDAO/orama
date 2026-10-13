package hub

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// openUptimeDB is a SQLite database holding the real migration's table.
func openUptimeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ddl, err := os.ReadFile("../../../migrations/062_status_uptime.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	return db
}

func comps(states ...cluster.State) []cluster.Component {
	ids := []string{"gateway", "database", "chain"}
	var out []cluster.Component
	for i, s := range states {
		out = append(out, cluster.Component{ID: ids[i], State: s})
	}
	return out
}

func TestUptimeStoreRecord_countsMinutesPerHour(t *testing.T) {
	db := openUptimeDB(t)
	u := UptimeStore{DB: db}
	ctx := context.Background()
	at := time.Date(2026, 9, 27, 10, 5, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := u.Record(ctx, at.Add(time.Duration(i)*time.Minute), comps(cluster.StateOperational, cluster.StateDegraded)); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.Record(ctx, at.Add(4*time.Minute), comps(cluster.StateOutage, cluster.StateDegraded)); err != nil {
		t.Fatal(err)
	}
	var op, out int
	if err := db.QueryRow(`SELECT operational_minutes, outage_minutes FROM status_uptime_hourly WHERE component='gateway' AND hour='2026-09-27T10'`).Scan(&op, &out); err != nil {
		t.Fatal(err)
	}
	if op != 3 || out != 1 {
		t.Fatalf("gateway minutes = %d up / %d out, want 3/1", op, out)
	}
}

func TestUptimeStoreRecord_unknownStateNotRecorded(t *testing.T) {
	db := openUptimeDB(t)
	if err := (UptimeStore{DB: db}).Record(context.Background(), time.Now(), comps(cluster.StateUnknown)); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM status_uptime_hourly`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows for an unknown state, want 0", n)
	}
}

func TestUptimeStoreHistory_dailyPercentDegradedCountsUp(t *testing.T) {
	db := openUptimeDB(t)
	u := UptimeStore{DB: db}
	ctx := context.Background()
	day := time.Date(2026, 9, 26, 23, 0, 0, 0, time.UTC)
	// 3 operational + 1 degraded + 4 outage minutes on the 26th → 50%.
	for i, s := range []cluster.State{"operational", "operational", "operational", "degraded", "outage", "outage", "outage", "outage"} {
		if err := u.Record(ctx, day.Add(time.Duration(i)*time.Minute), comps(s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.Record(ctx, day.Add(2*time.Hour), comps(cluster.StateOperational)); err != nil {
		t.Fatal(err)
	}
	h, err := u.History(ctx, day.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	g := h["gateway"]
	if len(g) != 2 || g[0].Date != "2026-09-26" || g[0].UptimePct != 50 || g[1].UptimePct != 100 {
		t.Fatalf("history = %+v, want [26th 50%%, 27th 100%%]", g)
	}
}

func TestUptimeStorePrune_dropsRowsOutsideWindow(t *testing.T) {
	db := openUptimeDB(t)
	u := UptimeStore{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -HistoryDays-1)
	if err := u.Record(ctx, old, comps(cluster.StateOperational)); err != nil {
		t.Fatal(err)
	}
	if err := u.Record(ctx, now, comps(cluster.StateOperational)); err != nil {
		t.Fatal(err)
	}
	if err := u.Prune(ctx, now); err != nil {
		t.Fatal(err)
	}
	var hours []string
	rows, _ := db.Query(`SELECT hour FROM status_uptime_hourly`)
	for rows.Next() {
		var h string
		rows.Scan(&h)
		hours = append(hours, h)
	}
	rows.Close()
	if strings.Join(hours, ",") != "2026-09-27T00" {
		t.Fatalf("hours left = %v", hours)
	}
}

func TestUptimeStore_closedDatabaseErrors(t *testing.T) {
	db := openUptimeDB(t)
	db.Close()
	u := UptimeStore{DB: db}
	if err := u.Record(context.Background(), time.Now(), comps(cluster.StateOperational)); err == nil {
		t.Error("Record on a closed database returned no error")
	}
	if _, err := u.History(context.Background(), time.Now()); err == nil {
		t.Error("History on a closed database returned no error")
	}
}

func TestIsUptimeWriter_lowestActiveNode(t *testing.T) {
	peers := []Peer{{ID: "a", Status: "inactive"}, {ID: "b", Status: "active"}, {ID: "c", Status: "active"}}
	if !IsUptimeWriter("b", peers) || IsUptimeWriter("c", peers) || IsUptimeWriter("a", peers) {
		t.Fatal("the writer must be the lowest-id active node, b")
	}
	if IsUptimeWriter("b", nil) {
		t.Fatal("no peers means no writer")
	}
}
