package nodenames

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	for _, name := range []string{"alice", "node-7", "a1b", strings.Repeat("a", MaxNameLen)} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{
		"", "ab", strings.Repeat("a", MaxNameLen+1), "Alice", "-alice", "alice-", "al_ice", "al.ice", "xn--abc",
		"ns1", "ns", "seed", "seed12", "a b",
	} {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) succeeded", name)
		}
	}
}

func TestDesired_publishesOneRecordPerPublicAddress(t *testing.T) {
	records, refused := Desired([]Named{
		{Name: "alice", IPs: []string{"93.184.216.34", "2606:4700:4700::1111"}},
		{Name: "bob-node", IPs: []string{"1.1.1.1"}},
	}, "stagenet.orama.network")
	want := []Record{
		{"alice.stagenet.orama.network.", "A", "93.184.216.34"},
		{"alice.stagenet.orama.network.", "AAAA", "2606:4700:4700::1111"},
		{"bob-node.stagenet.orama.network.", "A", "1.1.1.1"},
	}
	if len(records) != len(want) {
		t.Fatalf("records = %v, want %v (refused %v)", records, want, refused)
	}
	for i := range want {
		if records[i] != want[i] {
			t.Errorf("record %d = %v, want %v", i, records[i], want[i])
		}
	}
}

func TestDesired_refusesWhatMustNotBePublished(t *testing.T) {
	records, refused := Desired([]Named{
		{Name: "alice", IPs: []string{"10.0.0.7", "192.168.1.1", "127.0.0.1", "169.254.1.1", "198.18.0.5", "fd00::1", "::1", "0.0.0.0", "224.0.0.1", "not-an-ip", "", "93.184.216.34"}},
		{Name: "ns1", IPs: []string{"93.184.216.50"}},
		{Name: "Bad_Name", IPs: []string{"93.184.216.51"}},
		{Name: "empty", IPs: nil},
	}, "stagenet.orama.network")
	if len(records) != 1 || records[0].Value != "93.184.216.34" {
		t.Fatalf("records = %v, want only 93.184.216.34", records)
	}
	if len(refused) != 13 {
		t.Errorf("refused %d, want 13: %v", len(refused), refused)
	}
	for _, r := range refused {
		if r.Reason == "" || r.String() == "" {
			t.Errorf("refusal without a reason: %+v", r)
		}
	}
}

func TestDesired_normalisesAndDeduplicates(t *testing.T) {
	records, _ := Desired([]Named{
		{Name: "alice", IPs: []string{"93.184.216.34", "93.184.216.34", "::ffff:93.184.216.34", "2606:4700:4700:0000:0000:0000:0000:1111", "2606:4700:4700::1111"}},
	}, "stagenet.orama.network")
	if len(records) != 2 || records[0].Value != "93.184.216.34" || records[1].Value != "2606:4700:4700::1111" {
		t.Fatalf("records = %v", records)
	}
}

func TestDesired_emptyInput(t *testing.T) {
	records, refused := Desired(nil, "stagenet.orama.network")
	if len(records) != 0 || len(refused) != 0 {
		t.Fatalf("records %v refused %v", records, refused)
	}
}
