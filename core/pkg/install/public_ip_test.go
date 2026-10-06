package install

import "testing"

func TestValidatePublicIP(t *testing.T) {
	for _, ok := range []string{"203.0.113.7", "51.195.109.238"} {
		if err := ValidatePublicIP(ok); err != nil {
			t.Errorf("%s must pass: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "not-an-ip",
		"10.0.0.2",     // WireGuard / private
		"192.168.1.10", // private
		"100.64.3.4",   // carrier-grade NAT
		"127.0.0.1",    // loopback
		"0.0.0.0",      // unspecified
		"169.254.1.1",  // link-local
		"224.0.0.1",    // multicast
		"2001:db8::1",  // IPv6: a join URL is https://<ip>
	} {
		if err := ValidatePublicIP(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
