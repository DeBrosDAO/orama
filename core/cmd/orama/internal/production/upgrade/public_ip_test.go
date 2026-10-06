package upgrade

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func recordedIP(ip string) func() (string, error) { return func() (string, error) { return ip, nil } }

func detected(ip string) func() (net.IP, error) {
	return func() (net.IP, error) { return net.ParseIP(ip), nil }
}

func mustNotDetect(t *testing.T) func() (net.IP, error) {
	return func() (net.IP, error) {
		t.Error("detection ran although an address was already known")
		return nil, errors.New("unexpected")
	}
}

func TestResolvePublicIP_precedence(t *testing.T) {
	for _, tc := range []struct {
		name, flag, recorded string
		detect               func() (net.IP, error)
		want                 string
	}{
		{"the operator's flag wins", "203.0.113.7", "198.51.100.1", mustNotDetect(t), "203.0.113.7"},
		{"the recorded address is kept", "", "198.51.100.1", mustNotDetect(t), "198.51.100.1"},
		{"a node that never recorded one detects it", "", "", detected("198.51.100.9"), "198.51.100.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePublicIP(tc.flag, recordedIP(tc.recorded), tc.detect)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// The upgrade fails rather than writing a node.yaml invite cannot use.
func TestResolvePublicIP_failsWhenNoPublicAddressIsKnown(t *testing.T) {
	for _, tc := range []struct {
		name, flag, recorded string
		detect               func() (net.IP, error)
	}{
		{"no route", "", "", func() (net.IP, error) { return nil, errors.New("network is unreachable") }},
		{"behind NAT", "", "", detected("10.0.0.5")},
		{"WireGuard address recorded", "", "10.0.0.2", mustNotDetect(t)},
		{"garbage recorded", "", "not-an-ip", mustNotDetect(t)},
		{"loopback flag", "127.0.0.1", "", mustNotDetect(t)},
		{"IPv6 flag", "2001:db8::1", "", mustNotDetect(t)},
		{"carrier-grade NAT detected", "", "", detected("100.64.0.9")},
		{"unspecified flag", "0.0.0.0", "", mustNotDetect(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolvePublicIP(tc.flag, recordedIP(tc.recorded), tc.detect)
			if err == nil {
				t.Fatal("expected the upgrade to fail")
			}
			if tc.flag == "" && !strings.Contains(err.Error(), "--public-ip") {
				t.Errorf("the error must tell the operator how to fix it: %v", err)
			}
		})
	}
}

func TestResolvePublicIP_unreadableNodeYAMLIsAnError(t *testing.T) {
	_, err := resolvePublicIP("", func() (string, error) { return "", errors.New("parse node.yaml") }, mustNotDetect(t))
	if err == nil {
		t.Fatal("an unreadable node.yaml must fail the upgrade")
	}
}
