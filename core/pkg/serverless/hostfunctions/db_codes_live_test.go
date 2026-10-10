package hostfunctions

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
)

// bugboard #267 against a real rqlite: the code a guest gets for a failure
// through db_execute_v2 and db_query_v2, which run on the database/sql driver.

func execV2(t *testing.T, h *HostFunctions, sql string, args ...interface{}) dbExecuteV2Result {
	t.Helper()
	out, err := h.DBExecuteV2(nsCtx(), sql, args)
	if err != nil {
		t.Fatalf("DBExecuteV2(%q): Go error %v; SQL failures belong in the envelope", sql, err)
	}
	var res dbExecuteV2Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return res
}

func queryV2(t *testing.T, h *HostFunctions, sql string) dbQueryV2Result {
	t.Helper()
	out, err := h.DBQueryV2(nsCtx(), sql, nil)
	if err != nil {
		t.Fatalf("DBQueryV2(%q): Go error %v; SQL failures belong in the envelope", sql, err)
	}
	var res dbQueryV2Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return res
}

func TestDBV2_liveCodes(t *testing.T) {
	h := newHFWithDB(rqlitetest.Start(t))

	if res := execV2(t, h, "CREATE TABLE payments (id INTEGER PRIMARY KEY, tx TEXT NOT NULL UNIQUE, timeout TEXT)"); res.Error != "" {
		t.Fatalf("create table: %+v", res)
	}
	if first := execV2(t, h, "INSERT INTO payments (tx) VALUES (?)", "sig-1"); first.Error != "" || first.RowsAffected != 1 {
		t.Fatalf("first insert = %+v, want 1 row", first)
	}

	dup := execV2(t, h, "INSERT INTO payments (tx) VALUES (?)", "sig-1")
	if dup.Code != rqlite.BatchCodeConstraintViolation || !strings.Contains(dup.Error, "UNIQUE constraint failed: payments.tx") {
		t.Errorf("duplicate insert = %+v, want %s carrying SQLite's reason", dup, rqlite.BatchCodeConstraintViolation)
	}

	for _, sql := range []string{"INSERT INTO nosuch VALUES (1)", "INSERT INTO geofences VALUES (1)", "UPDATE payments SET eof = 1"} {
		if res := execV2(t, h, sql); res.Code != rqlite.BatchCodeInternal {
			t.Errorf("%s: code = %q (error %q), want %q", sql, res.Code, res.Error, rqlite.BatchCodeInternal)
		}
	}
	for _, sql := range []string{"SELECT * FROM nosuch", "SELECT deadline_exceeded FROM payments"} {
		if res := queryV2(t, h, sql); res.Code != rqlite.BatchCodeInternal || res.Error == "" {
			t.Errorf("%s: %+v, want an error coded %q", sql, res, rqlite.BatchCodeInternal)
		}
	}
}

func TestDBExecuteV2_liveRefusesANestedArgument(t *testing.T) {
	// database/sql refuses an object or array argument. rqlite's native API
	// would store an object as NULL and an array as a blob, so a path that
	// bypassed database/sql would silently write the wrong data.
	h := newHFWithDB(rqlitetest.Start(t))
	if res := execV2(t, h, "CREATE TABLE docs (id INTEGER PRIMARY KEY, body TEXT)"); res.Error != "" {
		t.Fatalf("create table: %+v", res)
	}

	for _, arg := range []interface{}{map[string]interface{}{"a": 1.0}, []interface{}{1.0, 2.0}} {
		if res := execV2(t, h, "INSERT INTO docs (body) VALUES (?)", arg); res.Error == "" {
			t.Errorf("nested argument %v was accepted: %+v", arg, res)
		}
	}
	if rows := queryV2(t, h, "SELECT COUNT(*) AS n FROM docs"); len(rows.Rows) != 1 || rows.Rows[0]["n"] != float64(0) {
		t.Fatalf("a refused write left rows behind: %+v", rows)
	}
}
