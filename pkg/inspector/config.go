package inspector

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Node represents a remote node parsed from nodes.conf.
type Node struct {
	Environment string // devnet, testnet
	User        string // SSH user
	Host        string // IP or hostname
	Role        string // node, nameserver-ns1, nameserver-ns2, nameserver-ns3
	SSHKey      string // populated at runtime by PrepareNodeKeys()
}

// Name returns a short display name for the node (user@host).
func (n Node) Name() string {
	return fmt.Sprintf("%s@%s", n.User, n.Host)
}

// IsNameserver returns true if the node has a nameserver role.
func (n Node) IsNameserver() bool {
	return strings.HasPrefix(n.Role, "nameserver")
}

// LoadNodes parses a nodes.conf file into a slice of Nodes.
// Format: environment|user@host|role
func LoadNodes(path string) ([]Node, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var nodes []Node
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 3 {
			return nil, fmt.Errorf("line %d: expected 3 pipe-delimited fields (env|user@host|role), got %d", lineNum, len(parts))
		}

		env := parts[0]
		userHost := parts[1]
		role := parts[2]

		// Parse user@host
		at := strings.LastIndex(userHost, "@")
		if at < 0 {
			return nil, fmt.Errorf("line %d: expected user@host format, got %q", lineNum, userHost)
		}
		user := userHost[:at]
		host := userHost[at+1:]

		nodes = append(nodes, Node{
			Environment: env,
			User:        user,
			Host:        host,
			Role:        role,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return nodes, nil
}

// FilterByEnv returns only nodes matching the given environment.
func FilterByEnv(nodes []Node, env string) []Node {
	var filtered []Node
	for _, n := range nodes {
		if n.Environment == env {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

// FilterByRole returns only nodes matching the given role prefix.
func FilterByRole(nodes []Node, rolePrefix string) []Node {
	var filtered []Node
	for _, n := range nodes {
		if strings.HasPrefix(n.Role, rolePrefix) {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

// RegularNodes returns non-nameserver nodes.
func RegularNodes(nodes []Node) []Node {
	var filtered []Node
	for _, n := range nodes {
		if n.Role == "node" {
			filtered = append(filtered, n)
		}
	}
	return filtered
}
