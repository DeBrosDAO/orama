package repair

import (
	"errors"
	"testing"
)

func TestFirstHTTPEndpoint_keepsSchemeHostAndPathOnly(t *testing.T) {
	cases := map[string]struct {
		endpoints []string
		want      string
	}{
		"plain":                {[]string{"https://node.example:443"}, "https://node.example:443"},
		"trailing slash":       {[]string{"https://node.example/"}, "https://node.example"},
		"path kept":            {[]string{"http://node.example:8080/ipfs/"}, "http://node.example:8080/ipfs"},
		"query dropped":        {[]string{"https://node.example/x?token=1&a=b"}, "https://node.example/x"},
		"fragment dropped":     {[]string{"https://node.example/x#frag"}, "https://node.example/x"},
		"userinfo dropped":     {[]string{"https://user:pass@node.example/x"}, "https://node.example/x"},
		"skips multiaddr":      {[]string{"/dns4/x/tcp/1", "https://node.example"}, "https://node.example"},
		"skips a hostless url": {[]string{"https:///path", "https://node.example"}, "https://node.example"},
	}
	for name, tc := range cases {
		got, err := FirstHTTPEndpoint("n1", tc.endpoints)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", name, got, err, tc.want)
		}
	}
	for _, none := range [][]string{nil, {"/dns4/x/tcp/1"}, {"ftp://node.example"}, {"https://"}} {
		if got, err := FirstHTTPEndpoint("n1", none); err == nil {
			t.Errorf("%v: got %q, want an error", none, got)
		}
	}
}

func TestRefuseNonPublic_coversEverySpecialRange(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:80", "10.1.2.3:80", "172.16.0.1:80", "192.168.1.1:80", "169.254.169.254:80", "0.0.0.0:80",
		"100.64.0.1:80", "198.18.0.1:80", "192.0.0.9:80", "240.0.0.1:80", "255.255.255.255:80", "224.0.0.1:80",
		"192.0.2.1:80", "198.51.100.1:80", "203.0.113.1:80",
		"[::1]:80", "[fe80::1]:80", "[fc00::1]:80", "[64:ff9b::7f00:1]:80", "[2001:db8::1]:80", "[ff02::1]:80",
		"[::ffff:10.0.0.1]:80", "[fe80::1%eth0]:80", "not-an-address",
	} {
		if err := refuseNonPublic("tcp", addr, nil); !errors.Is(err, ErrNotPublic) {
			t.Errorf("%s: got %v, want ErrNotPublic", addr, err)
		}
	}
	for _, addr := range []string{"8.8.8.8:443", "[2606:4700:4700::1111]:443", "100.63.255.255:80"} {
		if err := refuseNonPublic("tcp", addr, nil); err != nil {
			t.Errorf("%s: got %v, want nil", addr, err)
		}
	}
}
