package inspector

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNodes(t *testing.T) {
	content := `# Comment line
devnet|ubuntu@1.2.3.4|node
devnet|ubuntu@1.2.3.5|node
devnet|ubuntu@5.6.7.8|nameserver-ns1
`
	path := writeTempFile(t, content)

	nodes, err := LoadNodes(path)
	if err != nil {
		t.Fatalf("LoadNodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d", len(nodes))
	}

	// First node
	n := nodes[0]
	if n.Environment != "devnet" {
		t.Errorf("node[0].Environment = %q, want devnet", n.Environment)
	}
	if n.User != "ubuntu" {
		t.Errorf("node[0].User = %q, want ubuntu", n.User)
	}
	if n.Host != "1.2.3.4" {
		t.Errorf("node[0].Host = %q, want 1.2.3.4", n.Host)
	}
	if n.Role != "node" {
		t.Errorf("node[0].Role = %q, want node", n.Role)
	}
	if n.SSHKey != "" {
		t.Errorf("node[0].SSHKey = %q, want empty (set at runtime)", n.SSHKey)
	}

	// Third node with nameserver role
	n3 := nodes[2]
	if n3.Role != "nameserver-ns1" {
		t.Errorf("node[2].Role = %q, want nameserver-ns1", n3.Role)
	}
}

func TestLoadNodes_EmptyLines(t *testing.T) {
	content := `
# Full line comment

devnet|ubuntu@1.2.3.4|node

# Another comment
devnet|ubuntu@1.2.3.5|node
`
	path := writeTempFile(t, content)

	nodes, err := LoadNodes(path)
	if err != nil {
		t.Fatalf("LoadNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes (blank/comment lines skipped), got %d", len(nodes))
	}
}

func TestLoadNodes_InvalidFormat(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"too few fields", "devnet|ubuntu@1.2.3.4\n"},
		{"no @ in userhost", "devnet|localhost|node\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempFile(t, tt.content)
			_, err := LoadNodes(path)
			if err == nil {
				t.Error("expected error for invalid format")
			}
		})
	}
}

func TestLoadNodes_FileNotFound(t *testing.T) {
	_, err := LoadNodes("/nonexistent/path/file.conf")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFilterByEnv(t *testing.T) {
	nodes := []Node{
		{Environment: "devnet", Host: "1.1.1.1"},
		{Environment: "testnet", Host: "2.2.2.2"},
		{Environment: "devnet", Host: "3.3.3.3"},
	}
	filtered := FilterByEnv(nodes, "devnet")
	if len(filtered) != 2 {
		t.Fatalf("want 2 devnet nodes, got %d", len(filtered))
	}
	for _, n := range filtered {
		if n.Environment != "devnet" {
			t.Errorf("got env=%s, want devnet", n.Environment)
		}
	}
}

func TestFilterByRole(t *testing.T) {
	nodes := []Node{
		{Role: "node", Host: "1.1.1.1"},
		{Role: "nameserver-ns1", Host: "2.2.2.2"},
		{Role: "nameserver-ns2", Host: "3.3.3.3"},
		{Role: "node", Host: "4.4.4.4"},
	}
	filtered := FilterByRole(nodes, "nameserver")
	if len(filtered) != 2 {
		t.Fatalf("want 2 nameserver nodes, got %d", len(filtered))
	}
}

func TestRegularNodes(t *testing.T) {
	nodes := []Node{
		{Role: "node", Host: "1.1.1.1"},
		{Role: "nameserver-ns1", Host: "2.2.2.2"},
		{Role: "node", Host: "3.3.3.3"},
	}
	regular := RegularNodes(nodes)
	if len(regular) != 2 {
		t.Fatalf("want 2 regular nodes, got %d", len(regular))
	}
}

func TestNode_Name(t *testing.T) {
	n := Node{User: "ubuntu", Host: "1.2.3.4"}
	if got := n.Name(); got != "ubuntu@1.2.3.4" {
		t.Errorf("Name() = %q, want ubuntu@1.2.3.4", got)
	}
}

func TestNode_IsNameserver(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{"nameserver-ns1", true},
		{"nameserver-ns2", true},
		{"node", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			n := Node{Role: tt.role}
			if got := n.IsNameserver(); got != tt.want {
				t.Errorf("IsNameserver(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test-nodes.conf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}
