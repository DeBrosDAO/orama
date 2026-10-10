package utils

import (
	"bufio"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/unitenv"
	"gopkg.in/yaml.v3"
)

const (
	// olricReadyTimeout bounds the wait for one namespace's Olric memberlist
	// after its restart. The wait is an optimisation (see StartServicesOrdered),
	// so running out of it is a warning.
	olricReadyTimeout = 30 * time.Second

	// tcpDialTimeout is one connection attempt; tcpPollInterval the pause
	// between attempts.
	tcpDialTimeout  = 2 * time.Second
	tcpPollInterval = 1 * time.Second
)

// getOlricMemberlistAddr reads a namespace's Olric config, named by its unit's
// env file in envDir (unitenv.Dir), and returns the host:port its memberlist
// listens on.
//
// The host is memberlist.bindAddr, not localhost: namespace and index Olric
// bind the node's WireGuard address only (10.0.0.x), so a dial to localhost
// never connects and the wait always ran out its full budget. An unset or
// wildcard bindAddr listens on every interface, loopback included.
//
// The config lives in the orama user's tree below root (the rootfs anchor), so
// it is read without following a symlink and only up to a size limit.
func getOlricMemberlistAddr(envDir string, root rootfs.Root, namespace string) (string, error) {
	envFile := unitenv.Path(envDir, namespace, "olric")
	configPath, err := readEnvValue(envFile, "OLRIC_SERVER_CONFIG")
	if err != nil {
		return "", err
	}

	configData, err := root.ReadFile(configPath, rootfs.SmallFileLimit)
	if err != nil {
		return "", fmt.Errorf("read Olric config %s: %w", configPath, err)
	}

	var cfg struct {
		Memberlist struct {
			BindAddr string `yaml:"bindAddr"`
			BindPort int    `yaml:"bindPort"`
		} `yaml:"memberlist"`
	}
	if err := yaml.Unmarshal(configData, &cfg); err != nil {
		return "", fmt.Errorf("parse Olric config %s: %w", configPath, err)
	}
	if cfg.Memberlist.BindPort <= 0 {
		return "", fmt.Errorf("no memberlist.bindPort in Olric config %s", configPath)
	}

	host := strings.TrimSpace(cfg.Memberlist.BindAddr)
	if host == "" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	switch {
	case ip == nil:
		// The config sits where its namespace's gateway can write: only an
		// address is dialled, never a name, and never one it echoes back.
		return "", fmt.Errorf("olric config %s has a memberlist.bindAddr that is not an IP address", configPath)
	case ip.IsUnspecified():
		ip = net.IPv4(127, 0, 0, 1)
	case !ip.IsLoopback() && !olricBindNet(netipFrom(ip)):
		return "", fmt.Errorf("olric config %s binds memberlist outside loopback and the WireGuard overlay", configPath)
	}
	return net.JoinHostPort(ip.String(), fmt.Sprint(cfg.Memberlist.BindPort)), nil
}

// readEnvValue returns KEY's value from a systemd EnvironmentFile.
func readEnvValue(envFile, key string) (string, error) {
	f, err := os.Open(envFile)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", envFile, err)
	}
	defer f.Close()

	prefix := key + "="
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, prefix) {
			if v := strings.TrimPrefix(line, prefix); v != "" {
				return v, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", envFile, err)
	}
	return "", fmt.Errorf("%s does not set %s", envFile, key)
}

// waitForTCPAddr polls addr until it accepts a TCP connection or the timeout
// expires. Each attempt is a real dial, so the wait ends as soon as the
// listener is up.
func waitForTCPAddr(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		conn, err := net.DialTimeout("tcp", addr, tcpDialTimeout)
		if err == nil {
			conn.Close()
			return nil
		}
		lastErr = err
		if time.Now().Add(tcpPollInterval).After(deadline) {
			return fmt.Errorf("%s did not accept connections within %s: %w", addr, timeout, lastErr)
		}
		time.Sleep(tcpPollInterval)
	}
}

// olricBindNet reports whether an address is one Olric may be dialled on: the
// WireGuard overlay. A variable so a test can admit the address it can bind.
var olricBindNet = func(a netip.Addr) bool { return constants.WireGuardOverlay().Contains(a) }

// netipFrom converts an address parsed by net to netip for prefix checks.
func netipFrom(ip net.IP) netip.Addr {
	a, _ := netip.AddrFromSlice(ip)
	return a.Unmap()
}
