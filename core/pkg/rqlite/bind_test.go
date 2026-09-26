package rqlite

import "testing"

func TestBindHost(t *testing.T) {
	tests := []struct {
		adv  string
		want string
	}{
		{"10.0.0.4:10100", "10.0.0.4"},
		{"10.0.0.4", "10.0.0.4"},
		{" 10.0.0.4:10100 ", "10.0.0.4"},
		{"localhost:10100", "localhost"},
		{"[fd00::4]:10100", "fd00::4"},
	}
	for _, tt := range tests {
		got, err := BindHost(tt.adv)
		if err != nil {
			t.Errorf("BindHost(%q) error: %v", tt.adv, err)
			continue
		}
		if got != tt.want {
			t.Errorf("BindHost(%q) = %q, want %q", tt.adv, got, tt.want)
		}
	}
}

// rqlited binds only its advertise host. An address with no usable host used
// to become 127.0.0.1, so rqlited listened where no peer and no client looked;
// it is now an error at start.
func TestBindHost_rejectsEmptyAndWildcard(t *testing.T) {
	for _, adv := range []string{"", "   ", ":10100", "0.0.0.0:10100", "0.0.0.0", "[::]:10100", "::"} {
		if got, err := BindHost(adv); err == nil {
			t.Errorf("BindHost(%q) = %q, want an error", adv, got)
		}
	}
}

func TestBindAddr(t *testing.T) {
	got, err := BindAddr("10.0.0.4:10100", 10200)
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.0.0.4:10200" {
		t.Fatalf("got %q, want the advertise host with the given port", got)
	}
	if _, err := BindAddr("0.0.0.0:10100", 10100); err == nil {
		t.Fatal("wildcard advertise accepted")
	}
}
