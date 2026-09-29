package netguard

import (
	"net"
	"testing"
)

func TestReserved(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "10.0.0.1", "172.16.5.5", "192.168.1.1", "169.254.169.254", "100.64.0.1", "100.127.255.255",
		"198.18.0.2", "198.19.255.255", "192.0.0.8", "192.0.2.1", "0.0.0.0", "255.255.255.255", "224.0.0.1", "240.0.0.1",
		"192.31.196.1", "192.52.193.1", "::1", "::", "fe80::1", "fc00::1", "ff02::1", "64:ff9b::7f00:1", "2002:7f00:1::1",
		"::ffff:10.0.0.1", "::ffff:198.18.0.2", "2001:db8::1",
	} {
		if !Reserved(net.ParseIP(s)) {
			t.Errorf("%s must be reserved", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "100.63.255.255", "100.128.0.1", "198.17.255.255", "198.20.0.1", "2606:4700:4700::1111"} {
		if Reserved(net.ParseIP(s)) {
			t.Errorf("%s must not be reserved", s)
		}
	}
	if !Reserved(nil) {
		t.Error("a nil address must be reserved")
	}
}
