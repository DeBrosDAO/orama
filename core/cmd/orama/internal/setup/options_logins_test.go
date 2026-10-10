package setup

import (
	"slices"
	"strings"
	"testing"
)

// Machines rented from several providers log in differently (root on one image, ubuntu on
// another): --ip <user>@<address> gives one machine its own login, the rest use --user.
func TestNormalize_perMachineLogins(t *testing.T) {
	o := Options{Name: "founder", User: "root", IPs: []string{"ubuntu@57.129.166.16", "161.97.184.199"}}
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(o.IPs, []string{"57.129.166.16", "161.97.184.199"}) {
		t.Fatalf("IPs %v", o.IPs)
	}
	if o.UserFor("57.129.166.16") != "ubuntu" || o.UserFor("161.97.184.199") != "root" {
		t.Fatalf("logins %q %q", o.UserFor("57.129.166.16"), o.UserFor("161.97.184.199"))
	}
	if cmd := o.CommandLine(); !strings.Contains(cmd, "--ip ubuntu@57.129.166.16") || !strings.Contains(cmd, "--ip 161.97.184.199") {
		t.Fatalf("the resume command lost a login: %s", cmd)
	}
}

func TestNormalize_aBadLoginIsRefused(t *testing.T) {
	for _, ip := range []string{"Bad User@57.129.166.16", "@57.129.166.16", "a;b@57.129.166.16"} {
		o := Options{Name: "founder", IPs: []string{ip}}
		if err := o.Normalize(); err == nil {
			t.Errorf("%q was accepted", ip)
		}
	}
}

func TestParseIPList_keepsTheLogins(t *testing.T) {
	got, err := ParseIPList("ubuntu@57.129.166.16, 161.97.184.199\nroot@161.97.151.255")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ubuntu@57.129.166.16", "161.97.184.199", "root@161.97.151.255"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
