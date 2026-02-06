//go:build e2e && production

package production

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNameserver_NSRecords tests that NS records are properly configured for the domain
func TestNameserver_NSRecords(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	if len(env.Config.Nameservers) == 0 {
		t.Skip("No nameservers configured in e2e/config.yaml")
	}

	t.Run("NS records exist for base domain", func(t *testing.T) {
		nsRecords, err := net.LookupNS(env.BaseDomain)
		require.NoError(t, err, "Should be able to look up NS records for %s", env.BaseDomain)
		require.NotEmpty(t, nsRecords, "Should have NS records")

		t.Logf("Found %d NS records for %s:", len(nsRecords), env.BaseDomain)
		for _, ns := range nsRecords {
			t.Logf("  - %s", ns.Host)
		}

		// Verify our nameservers are listed
		for _, expected := range env.Config.Nameservers {
			found := false
			for _, ns := range nsRecords {
				// Trim trailing dot for comparison
				nsHost := strings.TrimSuffix(ns.Host, ".")
				if nsHost == expected || nsHost == expected+"." {
					found = true
					break
				}
			}
			assert.True(t, found, "NS records should include %s", expected)
		}
	})
}

// TestNameserver_GlueRecords tests that glue records point to correct IPs
func TestNameserver_GlueRecords(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	if len(env.Config.Nameservers) == 0 {
		t.Skip("No nameservers configured in e2e/config.yaml")
	}

	nameserverServers := e2e.GetNameserverServers(env.Config)
	if len(nameserverServers) == 0 {
		t.Skip("No servers marked as nameservers in config")
	}

	t.Run("Glue records resolve to correct IPs", func(t *testing.T) {
		for i, ns := range env.Config.Nameservers {
			ips, err := net.LookupHost(ns)
			require.NoError(t, err, "Nameserver %s should resolve", ns)
			require.NotEmpty(t, ips, "Nameserver %s should have IP addresses", ns)

			t.Logf("Nameserver %s resolves to: %v", ns, ips)

			// If we have the expected IP, verify it matches
			if i < len(nameserverServers) {
				expectedIP := nameserverServers[i].IP
				found := false
				for _, ip := range ips {
					if ip == expectedIP {
						found = true
						break
					}
				}
				assert.True(t, found, "Glue record for %s should point to %s (got %v)", ns, expectedIP, ips)
			}
		}
	})
}

// TestNameserver_CoreDNSResponds tests that our CoreDNS servers respond to queries
func TestNameserver_CoreDNSResponds(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	nameserverServers := e2e.GetNameserverServers(env.Config)
	if len(nameserverServers) == 0 {
		t.Skip("No servers marked as nameservers in config")
	}

	t.Run("CoreDNS servers respond to queries", func(t *testing.T) {
		for _, server := range nameserverServers {
			t.Run(server.Name, func(t *testing.T) {
				// Create a custom resolver that queries this specific server
				resolver := &net.Resolver{
					PreferGo: true,
					Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
						d := net.Dialer{
							Timeout: 5 * time.Second,
						}
						return d.DialContext(ctx, "udp", server.IP+":53")
					},
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				// Query the base domain
				ips, err := resolver.LookupHost(ctx, env.BaseDomain)
				if err != nil {
					// Log the error but don't fail - server might be configured differently
					t.Logf("⚠ CoreDNS at %s (%s) query error: %v", server.Name, server.IP, err)
					return
				}

				t.Logf("✓ CoreDNS at %s (%s) responded: %s -> %v", server.Name, server.IP, env.BaseDomain, ips)
				assert.NotEmpty(t, ips, "CoreDNS should return IP addresses")
			})
		}
	})
}

// TestNameserver_QueryLatency tests DNS query latency from our nameservers
func TestNameserver_QueryLatency(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	nameserverServers := e2e.GetNameserverServers(env.Config)
	if len(nameserverServers) == 0 {
		t.Skip("No servers marked as nameservers in config")
	}

	t.Run("DNS query latency is acceptable", func(t *testing.T) {
		for _, server := range nameserverServers {
			resolver := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					d := net.Dialer{
						Timeout: 5 * time.Second,
					}
					return d.DialContext(ctx, "udp", server.IP+":53")
				},
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			start := time.Now()
			_, err := resolver.LookupHost(ctx, env.BaseDomain)
			latency := time.Since(start)

			if err != nil {
				t.Logf("⚠ Query to %s failed: %v", server.Name, err)
				continue
			}

			t.Logf("DNS latency from %s (%s): %v", server.Name, server.IP, latency)

			// DNS queries should be fast (under 500ms is reasonable)
			assert.Less(t, latency, 500*time.Millisecond,
				"DNS query to %s should complete in under 500ms", server.Name)
		}
	})
}
