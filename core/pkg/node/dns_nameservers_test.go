package node

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const testNSDomain = "stagenet.example.test"

// recordValues returns the active values of fqdn/rtype, sorted.
func recordValues(t *testing.T, db *sql.DB, fqdn, rtype string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT value FROM dns_records WHERE fqdn = ? AND record_type = ?`, fqdn, rtype)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// claimAs claims a slot for node with ip, failing the test on error.
func claimAs(t *testing.T, db *sql.DB, node, ip string) string {
	t.Helper()
	host, err := claimNameserverSlot(context.Background(), db, node, testNSDomain, ip)
	if err != nil {
		t.Fatalf("claim for %s: %v", node, err)
	}
	return host
}

func TestClaimNameserverSlot_claimsLowestFreeSlotAndWritesGlue(t *testing.T) {
	db := newDNSTestDB(t)
	if got := claimAs(t, db, "peer-a", "203.0.113.1"); got != "ns1" {
		t.Fatalf("first claim = %s, want ns1", got)
	}
	if got := claimAs(t, db, "peer-b", "203.0.113.2"); got != "ns2" {
		t.Fatalf("second claim = %s, want ns2", got)
	}
	if got := recordValues(t, db, "ns2."+testNSDomain+".", "A"); !reflect.DeepEqual(got, []string{"203.0.113.2"}) {
		t.Errorf("glue for ns2 = %v", got)
	}
	// A node that holds a slot keeps it.
	if got := claimAs(t, db, "peer-a", "203.0.113.1"); got != "ns1" {
		t.Errorf("re-claim = %s, want the held ns1", got)
	}
}

// Slots are not capped at three: a five-nameserver cluster gets ns1..ns5.
func TestClaimNameserverSlot_moreThanThreeNameservers(t *testing.T) {
	db := newDNSTestDB(t)
	for i := 1; i <= 5; i++ {
		got := claimAs(t, db, fmt.Sprintf("peer-%d", i), fmt.Sprintf("203.0.113.%d", i))
		if want := fmt.Sprintf("ns%d", i); got != want {
			t.Fatalf("claim %d = %s, want %s", i, got, want)
		}
	}
}

// A holder whose address changed has one glue record, not the old one beside
// the new.
func TestClaimNameserverSlot_addressChangeReplacesGlue(t *testing.T) {
	db := newDNSTestDB(t)
	claimAs(t, db, "peer-a", "203.0.113.1")
	claimAs(t, db, "peer-a", "203.0.113.9")
	if got := recordValues(t, db, "ns1."+testNSDomain+".", "A"); !reflect.DeepEqual(got, []string{"203.0.113.9"}) {
		t.Errorf("glue after the address change = %v, want only the new address", got)
	}
}

func TestClaimNameserverSlot_refusals(t *testing.T) {
	db := newDNSTestDB(t)
	if _, err := claimNameserverSlot(context.Background(), db, "", testNSDomain, "203.0.113.1"); err == nil {
		t.Error("a node with no peer id claimed a slot")
	}
	for i := 1; i <= maxNameserverSlots; i++ {
		claimAs(t, db, fmt.Sprintf("peer-%d", i), fmt.Sprintf("203.0.113.%d", i))
	}
	_, err := claimNameserverSlot(context.Background(), db, "one-too-many", testNSDomain, "203.0.113.200")
	if err == nil || !strings.Contains(err.Error(), "slots") {
		t.Errorf("claim with every slot held: %v", err)
	}
}

// The zone publishes exactly the glued slots: a one-nameserver cluster has one
// NS record, the ns2/ns3 the installer seeds are removed, and a claimed slot
// without glue is not published.
func TestReconcileNameserverRecords_publishesExactlyTheGluedSlots(t *testing.T) {
	db := newDNSTestDB(t)
	apex := testNSDomain + "."
	for i := 1; i <= 3; i++ {
		if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value) VALUES (?, 'NS', ?)`,
			apex, fmt.Sprintf("ns%d.%s.", i, testNSDomain)); err != nil {
			t.Fatal(err)
		}
	}
	claimAs(t, db, "peer-a", "203.0.113.1")
	if _, err := db.Exec(`INSERT INTO dns_nameservers (hostname, node_id, ip_address, domain) VALUES ('ns2', 'peer-b', '203.0.113.2', ?)`, testNSDomain); err != nil {
		t.Fatal(err)
	}

	if err := reconcileNameserverRecords(context.Background(), db, testNSDomain, "peer-a"); err != nil {
		t.Fatal(err)
	}
	if got, want := recordValues(t, db, apex, "NS"), []string{"ns1." + apex}; !reflect.DeepEqual(got, want) {
		t.Errorf("NS = %v, want %v", got, want)
	}

	claimAs(t, db, "peer-c", "203.0.113.3") // takes ns3, glued
	for i := 4; i <= 5; i++ {
		claimAs(t, db, fmt.Sprintf("peer-%d", i), fmt.Sprintf("203.0.113.%d", i))
	}
	if err := reconcileNameserverRecords(context.Background(), db, testNSDomain, "peer-a"); err != nil {
		t.Fatal(err)
	}
	want := []string{"ns1." + apex, "ns3." + apex, "ns4." + apex, "ns5." + apex}
	if got := recordValues(t, db, apex, "NS"); !reflect.DeepEqual(got, want) {
		t.Errorf("NS = %v, want %v", got, want)
	}
}

// The SOA names the lowest glued slot, and only its holder writes it.
func TestReconcileNameserverRecords_soaFollowsThePrimarySlot(t *testing.T) {
	db := newDNSTestDB(t)
	apex := testNSDomain + "."
	if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value) VALUES (?, 'SOA', ?)`,
		apex, "ns1."+apex+" admin."+apex+" 1 3600 1800 604800 300"); err != nil {
		t.Fatal(err)
	}
	// ns1 is claimed by a node that never wrote its glue; ns2 is the primary.
	if _, err := db.Exec(`INSERT INTO dns_nameservers (hostname, node_id, ip_address, domain) VALUES ('ns1', 'peer-x', '203.0.113.9', ?)`, testNSDomain); err != nil {
		t.Fatal(err)
	}
	claimAs(t, db, "peer-b", "203.0.113.2")

	if err := reconcileNameserverRecords(context.Background(), db, testNSDomain, "peer-other"); err != nil {
		t.Fatal(err)
	}
	if soa := recordValues(t, db, apex, "SOA"); len(soa) != 1 || !strings.HasPrefix(soa[0], "ns1.") {
		t.Fatalf("a node not holding the primary slot rewrote the SOA: %v", soa)
	}

	if err := reconcileNameserverRecords(context.Background(), db, testNSDomain, "peer-b"); err != nil {
		t.Fatal(err)
	}
	soa := recordValues(t, db, apex, "SOA")
	if len(soa) != 1 || !strings.HasPrefix(soa[0], "ns2."+apex+" admin."+apex+" ") {
		t.Fatalf("SOA = %v, want one naming ns2 as primary", soa)
	}

	// Converged: a second pass leaves it alone.
	if err := reconcileNameserverRecords(context.Background(), db, testNSDomain, "peer-b"); err != nil {
		t.Fatal(err)
	}
	if again := recordValues(t, db, apex, "SOA"); !reflect.DeepEqual(again, soa) {
		t.Errorf("a converged SOA was rewritten: %v -> %v", soa, again)
	}
}

// With no nameserver glued — all of them missed their heartbeat together, or
// none has claimed yet — the last known NS set stays rather than leaving the
// zone with none; it is corrected once a slot is glued again.
func TestReconcileNameserverRecords_noGluedSlotKeepsTheLastSet(t *testing.T) {
	db := newDNSTestDB(t)
	apex := testNSDomain + "."
	for _, ns := range []string{"ns1." + apex, "ns2." + apex} {
		if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value) VALUES (?, 'NS', ?)`, apex, ns); err != nil {
			t.Fatal(err)
		}
	}
	if err := reconcileNameserverRecords(context.Background(), db, testNSDomain, "peer-a"); err != nil {
		t.Fatal(err)
	}
	if got := recordValues(t, db, apex, "NS"); len(got) != 2 {
		t.Fatalf("NS = %v, want the last known set kept", got)
	}

	claimAs(t, db, "peer-a", "203.0.113.1")
	if err := reconcileNameserverRecords(context.Background(), db, testNSDomain, "peer-a"); err != nil {
		t.Fatal(err)
	}
	if got, want := recordValues(t, db, apex, "NS"), []string{"ns1." + apex}; !reflect.DeepEqual(got, want) {
		t.Errorf("NS = %v, want %v once a slot is glued", got, want)
	}
}

// A departed nameserver's slot and glue go, and its NS name leaves the zone
// on the next reconcile; other slots are untouched.
func TestReleaseNameserverSlot_removesSlotGlueAndNS(t *testing.T) {
	db := newDNSTestDB(t)
	apex := testNSDomain + "."
	claimAs(t, db, "peer-a", "203.0.113.1")
	claimAs(t, db, "peer-b", "203.0.113.2")
	if err := reconcileNameserverRecords(context.Background(), db, testNSDomain, "peer-a"); err != nil {
		t.Fatal(err)
	}

	if err := releaseNameserverSlot(context.Background(), db, "peer-b"); err != nil {
		t.Fatal(err)
	}
	if got := recordValues(t, db, "ns2."+apex, "A"); len(got) != 0 {
		t.Errorf("glue left behind: %v", got)
	}
	if got := recordValues(t, db, "ns1."+apex, "A"); len(got) != 1 {
		t.Errorf("another slot's glue was removed: %v", got)
	}
	if err := reconcileNameserverRecords(context.Background(), db, testNSDomain, "peer-a"); err != nil {
		t.Fatal(err)
	}
	if got, want := recordValues(t, db, apex, "NS"), []string{"ns1." + apex}; !reflect.DeepEqual(got, want) {
		t.Errorf("NS = %v, want %v", got, want)
	}
}

func TestSlotNumber(t *testing.T) {
	for in, want := range map[string]int{"ns1": 1, "ns13": 13, "ns0": 0, "ns": 0, "nsx": 0, "foo1": 0} {
		if got := slotNumber(in); got != want {
			t.Errorf("slotNumber(%q) = %d, want %d", in, got, want)
		}
	}
}

// Two SOA rows (two writers of an older binary) collapse to one, and the SOA
// is changed in place — the zone never goes without one.
func TestEnsureSOA_collapsesDuplicatesInPlace(t *testing.T) {
	db := newDNSTestDB(t)
	apex := testNSDomain + "."
	for _, serial := range []string{"1", "2"} {
		if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value) VALUES (?, 'SOA', ?)`,
			apex, "ns1."+apex+" admin."+apex+" "+serial+" 3600 1800 604800 300"); err != nil {
			t.Fatal(err)
		}
	}
	var firstID int
	if err := db.QueryRow(`SELECT MIN(id) FROM dns_records WHERE record_type = 'SOA'`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if err := ensureSOA(context.Background(), db, apex, "ns2."+apex); err != nil {
		t.Fatal(err)
	}
	var id int
	var value string
	if err := db.QueryRow(`SELECT id, value FROM dns_records WHERE record_type = 'SOA'`).Scan(&id, &value); err != nil {
		t.Fatalf("want exactly one SOA: %v", err)
	}
	if id != firstID || !strings.HasPrefix(value, "ns2."+apex+" ") {
		t.Errorf("SOA = (%d, %q), want row %d updated to name ns2", id, value, firstID)
	}
}

// A released slot's glue goes whatever address it names — including one that
// differs from what dns_nodes has for the departed node.
func TestReleaseNameserverSlot_removesGlueForAnyAddress(t *testing.T) {
	db := newDNSTestDB(t)
	claimAs(t, db, "peer-a", "203.0.113.1")
	if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value) VALUES (?, 'A', '198.51.100.7')`, "ns1."+testNSDomain+"."); err != nil {
		t.Fatal(err)
	}
	if err := releaseNameserverSlot(context.Background(), db, "peer-a"); err != nil {
		t.Fatal(err)
	}
	if got := recordValues(t, db, "ns1."+testNSDomain+".", "A"); len(got) != 0 {
		t.Errorf("glue left behind: %v", got)
	}
}

// A converged holder writes nothing: its slot row is not touched.
func TestClaimNameserverSlot_convergedHolderDoesNotWrite(t *testing.T) {
	db := newDNSTestDB(t)
	claimAs(t, db, "peer-a", "203.0.113.1")
	if _, err := db.Exec(`UPDATE dns_nameservers SET updated_at = '2000-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	claimAs(t, db, "peer-a", "203.0.113.1")
	var updated string
	if err := db.QueryRow(`SELECT updated_at FROM dns_nameservers WHERE node_id = 'peer-a'`).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(updated, "2000-01-01") {
		t.Errorf("a converged slot was rewritten (updated_at %s)", updated)
	}
}

// An NS row that was deactivated is not counted as published: it is
// reactivated rather than left dark.
func TestReconcileNameserverRecords_reactivatesADarkNSRecord(t *testing.T) {
	db := newDNSTestDB(t)
	apex := testNSDomain + "."
	claimAs(t, db, "peer-a", "203.0.113.1")
	if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value, is_active) VALUES (?, 'NS', ?, FALSE)`, apex, "ns1."+apex); err != nil {
		t.Fatal(err)
	}
	if err := reconcileNameserverRecords(context.Background(), db, testNSDomain, "peer-a"); err != nil {
		t.Fatal(err)
	}
	var active bool
	if err := db.QueryRow(`SELECT is_active FROM dns_records WHERE fqdn = ? AND record_type = 'NS'`, apex).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Error("the NS record for a glued slot stayed inactive")
	}
}
