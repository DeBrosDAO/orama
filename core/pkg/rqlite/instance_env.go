package rqlite

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Env keys the systemd spawner writes into an instance's rqlite.env.
const (
	instanceEnvFileName    = "rqlite.env"
	instanceEnvHTTPAddr    = "HTTP_ADDR"
	instanceEnvHTTPAdvAddr = "HTTP_ADV_ADDR"
)

// InstanceEnvFile is the rqlite.env of namespace ns under envDir (unitenv.Dir
// on a node: the root-owned tree the units read their env files from).
func InstanceEnvFile(envDir, ns string) string {
	return filepath.Join(envDir, ns, instanceEnvFileName)
}

// InstanceAddrFromEnv returns the host:port an rqlite instance can be reached
// on, from the rqlite.env the spawner wrote for it. ok is false when the file
// does not exist (no rqlite instance for that namespace on this node).
//
// HTTP_ADDR is what rqlited binds. Instances spawned before rqlited was bound
// to the WireGuard IP have HTTP_ADDR=0.0.0.0:<port> — rqlite.env is only
// rewritten on respawn — and a wildcard is not an address to dial. Such an
// instance listens on every interface, so its advertise address
// (HTTP_ADV_ADDR, the node's WireGuard IP) reaches it and is used instead.
func InstanceAddrFromEnv(envFile string) (addr string, ok bool, err error) {
	vars, err := readEnvFile(envFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read %s: %w", envFile, err)
	}
	bind := vars[instanceEnvHTTPAddr]
	if bind == "" {
		return "", true, fmt.Errorf("%s has no %s", envFile, instanceEnvHTTPAddr)
	}
	if !isWildcardAddr(bind) {
		return bind, true, nil
	}
	adv := vars[instanceEnvHTTPAdvAddr]
	if adv == "" || isWildcardAddr(adv) {
		return "", true, fmt.Errorf("%s binds the wildcard %s and has no usable %s to reach it on", envFile, bind, instanceEnvHTTPAdvAddr)
	}
	return adv, true, nil
}

// InstanceEndpointFromEnv is the Endpoint of the rqlite instance described by
// envFile, with the given (cluster-wide) credentials. ok as in
// InstanceAddrFromEnv.
func InstanceEndpointFromEnv(envFile, username, password string) (Endpoint, bool, error) {
	addr, ok, err := InstanceAddrFromEnv(envFile)
	if err != nil || !ok {
		return Endpoint{}, ok, err
	}
	ep, err := NewEndpoint(addr, username, password)
	if err != nil {
		return Endpoint{}, true, fmt.Errorf("%s: %w", envFile, err)
	}
	return ep, true, nil
}

// isWildcardAddr reports whether host:port has an empty or unspecified host.
func isWildcardAddr(hostPort string) bool {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return false // not host:port; NewEndpoint reports it
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// readEnvFile parses KEY=value lines, skipping blanks and # comments.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	vars := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, found := strings.Cut(line, "="); found {
			vars[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return vars, nil
}
