package monitor

import (
	"reflect"
	"testing"
)

const sampleRows = `[
 {"namespace":"a","host":"10.0.0.2","rqlite_up":true,"olric_up":true,"gateway_up":true},
 {"namespace":"a","host":"10.0.0.1","rqlite_up":true},
 {"namespace":"b","host":"10.0.0.3"},
 {"namespace":"a","host":"10.0.0.1"}
]`

func TestHostsOf_membersOnlySortedAndUnique(t *testing.T) {
	rows, err := ParseNamespaceRows([]byte(sampleRows))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := HostsOf(rows, "a"), []string{"10.0.0.1", "10.0.0.2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("HostsOf(a) = %v, want %v", got, want)
	}
	if got := HostsOf(rows, "b"); !reflect.DeepEqual(got, []string{"10.0.0.3"}) {
		t.Errorf("HostsOf(b) = %v", got)
	}
}

func TestHostsOf_unknownNamespaceAndNoRows(t *testing.T) {
	rows, err := ParseNamespaceRows([]byte(sampleRows))
	if err != nil {
		t.Fatal(err)
	}
	if got := HostsOf(rows, "missing"); len(got) != 0 {
		t.Errorf("HostsOf(missing) = %v, want none", got)
	}
	if got := HostsOf(nil, "a"); len(got) != 0 {
		t.Errorf("HostsOf(nil) = %v, want none", got)
	}
	empty, err := ParseNamespaceRows([]byte(`[]`))
	if err != nil || len(empty) != 0 {
		t.Errorf("empty rows: %v %v", empty, err)
	}
}

func TestParseNamespaceRows_notJSON(t *testing.T) {
	if _, err := ParseNamespaceRows([]byte("no namespaces")); err == nil {
		t.Fatal("parsed plain text")
	}
}
