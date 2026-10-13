package node

import (
	"context"
	"database/sql"
	"testing"
)

func seedA(t *testing.T, db *sql.DB, fqdn, ip, ns string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value, namespace) VALUES (?, 'A', ?, ?)`, fqdn, ip, ns); err != nil {
		t.Fatal(err)
	}
}

func hasA(t *testing.T, db *sql.DB, fqdn, ip string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE fqdn = ? AND value = ? AND record_type = 'A'`, fqdn, ip).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

var testBase = []string{"example.test.", "*.example.test."}

func TestWithdrawOwnRecords_leavesTheBaseAndGatewayRoundRobins(t *testing.T) {
	db := newDNSTestDB(t)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
		seedA(t, db, "example.test.", ip, "system")
		seedA(t, db, "*.example.test.", ip, "system")
		seedA(t, db, "ns-acme.example.test.", ip, "namespace:acme")
		seedA(t, db, "*.ns-acme.example.test.", ip, "namespace:acme")
	}
	removed, err := withdrawOwnRecords(context.Background(), db, testBase, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 4 {
		t.Fatalf("removed %d records, want 4", removed)
	}
	for _, f := range []string{"example.test.", "*.example.test.", "ns-acme.example.test.", "*.ns-acme.example.test."} {
		if hasA(t, db, f, "1.1.1.1") {
			t.Errorf("%s still answers the withdrawn node", f)
		}
		if !hasA(t, db, f, "2.2.2.2") {
			t.Errorf("%s lost the serving node", f)
		}
	}
}

func TestWithdrawOwnRecords_neverEmptiesAName(t *testing.T) {
	db := newDNSTestDB(t)
	seedA(t, db, "example.test.", "1.1.1.1", "system")
	seedA(t, db, "ns-solo.example.test.", "1.1.1.1", "namespace:solo")
	if _, err := withdrawOwnRecords(context.Background(), db, testBase, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if !hasA(t, db, "example.test.", "1.1.1.1") || !hasA(t, db, "ns-solo.example.test.", "1.1.1.1") {
		t.Fatal("the last address of a name was withdrawn: the name would answer nothing")
	}
}

func TestWithdrawOwnRecords_leavesTURNAndOtherNodes(t *testing.T) {
	db := newDNSTestDB(t)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
		seedA(t, db, "turn-acme.example.test.", ip, "namespace:acme")
		seedA(t, db, "app.example.test.", ip, "deployment")
	}
	removed, err := withdrawOwnRecords(context.Background(), db, testBase, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 || !hasA(t, db, "turn-acme.example.test.", "1.1.1.1") || !hasA(t, db, "app.example.test.", "1.1.1.1") {
		t.Fatalf("removed %d records: TURN, which does not go through Caddy, and non-system records are not this withdrawal's", removed)
	}
}

func TestWithdrawOwnRecords_noRecordsIsANoop(t *testing.T) {
	db := newDNSTestDB(t)
	if removed, err := withdrawOwnRecords(context.Background(), db, testBase, "1.1.1.1"); err != nil || removed != 0 {
		t.Fatalf("removed %d, err %v on an empty table", removed, err)
	}
}

// The withdrawal is only safe because the heartbeat's re-advertisement brings
// back exactly what it removed once the edge serves again.
func TestWithdrawOwnRecords_reAdvertiseRestoresTheGatewayRecords(t *testing.T) {
	db := setupDNSTestDB(t)
	setupNamespaceClusterTables(t, db)
	for _, prefix := range []string{"", "*."} {
		for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
			mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES (?,'A',?,'namespace:anchat-test')`,
				prefix+"ns-anchat-test.d.", ip)
		}
	}
	if _, err := withdrawOwnRecords(context.Background(), db, []string{"d.", "*.d."}, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if countRecords(t, db, `value='1.1.1.1'`) != 0 {
		t.Fatal("the withdrawn node still has gateway records")
	}
	for _, prefix := range []string{"", "*."} {
		if got := runEnsure(t, db, prefix, "d", "1.1.1.1", "peerA"); got != 1 {
			t.Errorf("re-advertising %sns-anchat-test.d. added %d rows, want 1", prefix, got)
		}
	}
	if countRecords(t, db, `value='1.1.1.1' AND namespace='namespace:anchat-test'`) != 2 {
		t.Error("the re-advertisement did not restore both gateway names")
	}
}

func TestWithdrawOwnRecords_disabledRecordIsNotASurvivor(t *testing.T) {
	db := setupDNSTestDB(t)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('d.','A','1.1.1.1','system')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace,is_active) VALUES ('d.','A','2.2.2.2','system',FALSE)`)
	if _, err := withdrawOwnRecords(context.Background(), db, []string{"d."}, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if countRecords(t, db, `value='1.1.1.1'`) != 1 {
		t.Fatal("withdrawn behind a disabled record: the name would answer nothing")
	}
}
