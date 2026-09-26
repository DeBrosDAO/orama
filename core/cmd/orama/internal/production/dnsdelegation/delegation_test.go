package dnsdelegation

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func row(domain, host, ip string) []any { return []any{domain, host, ip} }

// Slots are grouped by domain and ordered by number, so ns10 follows ns2.
func TestFromRows_groupsAndOrdersSlots(t *testing.T) {
	got, err := FromRows([][]any{
		row("stagenet.example.test", "ns10", "203.0.113.10"),
		row("stagenet.example.test", "ns1", "203.0.113.1"),
		row("stagenet.example.test", "ns2", "203.0.113.2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Delegation{{Domain: "stagenet.example.test", Nameservers: []Nameserver{
		{"ns1", "203.0.113.1"}, {"ns2", "203.0.113.2"}, {"ns10", "203.0.113.10"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FromRows = %+v, want %+v", got, want)
	}
}

// No claimed slot is no delegation, not an error: the command says why.
func TestFromRows_empty(t *testing.T) {
	got, err := FromRows(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("FromRows(nil) = %v, %v", got, err)
	}
}

// What is printed is typed into a registrar, so anything the cluster would not
// have written is refused.
func TestFromRows_refusesWhatTheClusterWouldNotWrite(t *testing.T) {
	for name, r := range map[string][]any{
		"private glue": row("d.example.test", "ns1", "10.0.0.1"),
		"not an ip":    row("d.example.test", "ns1", "x; rm -rf"),
		"bad hostname": row("d.example.test", "www", "203.0.113.1"),
		"ns0":          row("d.example.test", "ns0", "203.0.113.1"),
		"no domain":    row("", "ns1", "203.0.113.1"),
		"short row":    {"d.example.test", "ns1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := FromRows([][]any{r}); err == nil {
				t.Errorf("row %v was accepted", r)
			}
		})
	}
}

func TestRecords_nsThenGlue(t *testing.T) {
	lines := Records(Delegation{Domain: "stagenet.example.test", Nameservers: []Nameserver{{"ns1", "203.0.113.1"}, {"ns2", "203.0.113.2"}}})
	want := []string{
		"stagenet.example.test.\tIN\tNS\tns1.stagenet.example.test.",
		"stagenet.example.test.\tIN\tNS\tns2.stagenet.example.test.",
		"ns1.stagenet.example.test.\tIN\tA\t203.0.113.1",
		"ns2.stagenet.example.test.\tIN\tA\t203.0.113.2",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("Records =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestParentZone(t *testing.T) {
	for in, want := range map[string]string{
		"stagenet.dbrsteting.bid": "dbrsteting.bid",
		"dbrsteting.bid":          "bid",
	} {
		if got := ParentZone(in); got != want {
			t.Errorf("ParentZone(%q) = %q, want %q", in, got, want)
		}
	}
}

// Only glued slots are delegated to — the same set the cluster's zone
// publishes. Run against the real tables' shape.
func TestQuery_onlyGluedSlots(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE dns_records (fqdn TEXT, record_type TEXT, value TEXT, is_active BOOLEAN NOT NULL DEFAULT TRUE)`,
		`CREATE TABLE dns_nameservers (hostname TEXT PRIMARY KEY, node_id TEXT, ip_address TEXT, domain TEXT)`,
		`INSERT INTO dns_nameservers VALUES ('ns1','a','203.0.113.1','d.example.test'), ('ns2','b','203.0.113.2','d.example.test'), ('ns3','c','203.0.113.3','d.example.test')`,
		`INSERT INTO dns_records (fqdn, record_type, value) VALUES ('ns1.d.example.test.','A','203.0.113.1'), ('ns3.d.example.test.','A','198.51.100.9')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query(Query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var d, h, ip string
		if err := rows.Scan(&d, &h, &ip); err != nil {
			t.Fatal(err)
		}
		got = append(got, h)
	}
	if !reflect.DeepEqual(got, []string{"ns1"}) {
		t.Errorf("delegated %v, want only ns1 (ns2 has no glue, ns3's glue names another address)", got)
	}
}

// Glue is printed as the dotted quad an A record holds.
func TestFromRows_canonicalisesMappedIPv4(t *testing.T) {
	got, err := FromRows([][]any{row("d.example.test", "ns1", "::ffff:203.0.113.1")})
	if err != nil {
		t.Fatal(err)
	}
	if ip := got[0].Nameservers[0].IP; ip != "203.0.113.1" {
		t.Errorf("IP = %q", ip)
	}
}
