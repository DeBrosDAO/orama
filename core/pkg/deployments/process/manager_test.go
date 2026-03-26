package process

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

func TestNewManager(t *testing.T) {
	logger := zap.NewNop()
	m := NewManager(logger)

	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.logger == nil {
		t.Error("expected logger to be set")
	}
	if m.processes == nil {
		t.Error("expected processes map to be initialized")
	}
}

func TestGetServiceName(t *testing.T) {
	m := NewManager(zap.NewNop())

	tests := []struct {
		name      string
		namespace string
		deplName  string
		want      string
	}{
		{
			name:      "simple names",
			namespace: "alice",
			deplName:  "myapp",
			want:      "orama-deploy-alice-myapp",
		},
		{
			name:      "dots replaced with dashes",
			namespace: "alice.eth",
			deplName:  "my.app",
			want:      "orama-deploy-alice-eth-my-app",
		},
		{
			name:      "multiple dots",
			namespace: "a.b.c",
			deplName:  "x.y.z",
			want:      "orama-deploy-a-b-c-x-y-z",
		},
		{
			name:      "no dots unchanged",
			namespace: "production",
			deplName:  "api-server",
			want:      "orama-deploy-production-api-server",
		},
		{
			name:      "empty strings",
			namespace: "",
			deplName:  "",
			want:      "orama-deploy--",
		},
		{
			name:      "single character names",
			namespace: "a",
			deplName:  "b",
			want:      "orama-deploy-a-b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &deployments.Deployment{
				Namespace: tt.namespace,
				Name:      tt.deplName,
			}
			got := m.getServiceName(d)
			if got != tt.want {
				t.Errorf("getServiceName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetStartCommand(t *testing.T) {
	m := NewManager(zap.NewNop())
	// On macOS (test environment), useSystemd will be false, so node/npm use short paths.
	// We explicitly set it to test both modes.

	workDir := "/opt/orama/deployments/alice/myapp"

	tests := []struct {
		name       string
		useSystemd bool
		deplType   deployments.DeploymentType
		env        map[string]string
		want       string
	}{
		{
			name:       "nextjs without systemd",
			useSystemd: false,
			deplType:   deployments.DeploymentTypeNextJS,
			want:       "node server.js",
		},
		{
			name:       "nextjs with systemd",
			useSystemd: true,
			deplType:   deployments.DeploymentTypeNextJS,
			want:       "/usr/bin/node server.js",
		},
		{
			name:       "nodejs backend default entry point",
			useSystemd: false,
			deplType:   deployments.DeploymentTypeNodeJSBackend,
			want:       "node index.js",
		},
		{
			name:       "nodejs backend with systemd default entry point",
			useSystemd: true,
			deplType:   deployments.DeploymentTypeNodeJSBackend,
			want:       "/usr/bin/node index.js",
		},
		{
			name:       "nodejs backend with custom entry point",
			useSystemd: false,
			deplType:   deployments.DeploymentTypeNodeJSBackend,
			env:        map[string]string{"ENTRY_POINT": "src/server.js"},
			want:       "node src/server.js",
		},
		{
			name:       "nodejs backend with npm:start entry point",
			useSystemd: false,
			deplType:   deployments.DeploymentTypeNodeJSBackend,
			env:        map[string]string{"ENTRY_POINT": "npm:start"},
			want:       "npm start",
		},
		{
			name:       "nodejs backend with npm:start systemd",
			useSystemd: true,
			deplType:   deployments.DeploymentTypeNodeJSBackend,
			env:        map[string]string{"ENTRY_POINT": "npm:start"},
			want:       "/usr/bin/npm start",
		},
		{
			name:       "go backend",
			useSystemd: false,
			deplType:   deployments.DeploymentTypeGoBackend,
			want:       filepath.Join(workDir, "app"),
		},
		{
			name:       "static type falls to default",
			useSystemd: false,
			deplType:   deployments.DeploymentTypeStatic,
			want:       "echo 'Unknown deployment type'",
		},
		{
			name:       "unknown type falls to default",
			useSystemd: false,
			deplType:   deployments.DeploymentType("something-else"),
			want:       "echo 'Unknown deployment type'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.useSystemd = tt.useSystemd
			d := &deployments.Deployment{
				Type:        tt.deplType,
				Environment: tt.env,
			}
			got := m.getStartCommand(d, workDir)
			if got != tt.want {
				t.Errorf("getStartCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapRestartPolicy(t *testing.T) {
	m := NewManager(zap.NewNop())

	tests := []struct {
		name   string
		policy deployments.RestartPolicy
		want   string
	}{
		{
			name:   "always",
			policy: deployments.RestartPolicyAlways,
			want:   "always",
		},
		{
			name:   "on-failure",
			policy: deployments.RestartPolicyOnFailure,
			want:   "on-failure",
		},
		{
			name:   "never maps to no",
			policy: deployments.RestartPolicyNever,
			want:   "no",
		},
		{
			name:   "empty string defaults to on-failure",
			policy: deployments.RestartPolicy(""),
			want:   "on-failure",
		},
		{
			name:   "unknown policy defaults to on-failure",
			policy: deployments.RestartPolicy("unknown"),
			want:   "on-failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.mapRestartPolicy(tt.policy)
			if got != tt.want {
				t.Errorf("mapRestartPolicy(%q) = %q, want %q", tt.policy, got, tt.want)
			}
		})
	}
}

func TestParseSystemctlShow(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name:  "typical output",
			input: "ActiveState=active\nSubState=running\nMainPID=1234",
			want: map[string]string{
				"ActiveState": "active",
				"SubState":    "running",
				"MainPID":     "1234",
			},
		},
		{
			name:  "empty output",
			input: "",
			want:  map[string]string{},
		},
		{
			name:  "lines without equals sign are skipped",
			input: "ActiveState=active\nno-equals-here\nMainPID=5678",
			want: map[string]string{
				"ActiveState": "active",
				"MainPID":     "5678",
			},
		},
		{
			name:  "value containing equals sign",
			input: "Description=My App=Extra",
			want: map[string]string{
				"Description": "My App=Extra",
			},
		},
		{
			name:  "empty value",
			input: "MainPID=\nActiveState=active",
			want: map[string]string{
				"MainPID":     "",
				"ActiveState": "active",
			},
		},
		{
			name:  "value with whitespace is trimmed",
			input: "ActiveState= active \nMainPID= 1234 ",
			want: map[string]string{
				"ActiveState": "active",
				"MainPID":     "1234",
			},
		},
		{
			name:  "trailing newline",
			input: "ActiveState=active\n",
			want: map[string]string{
				"ActiveState": "active",
			},
		},
		{
			name:  "timestamp value with spaces",
			input: "ActiveEnterTimestamp=Mon 2026-01-29 10:00:00 UTC",
			want: map[string]string{
				"ActiveEnterTimestamp": "Mon 2026-01-29 10:00:00 UTC",
			},
		},
		{
			name:  "line with only equals sign is skipped",
			input: "=value",
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSystemctlShow(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("parseSystemctlShow() returned %d entries, want %d\ngot:  %v\nwant: %v",
					len(got), len(tt.want), got, tt.want)
				return
			}
			for k, wantV := range tt.want {
				gotV, ok := got[k]
				if !ok {
					t.Errorf("missing key %q in result", k)
					continue
				}
				if gotV != wantV {
					t.Errorf("key %q: got %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

func TestParseSystemdTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(t *testing.T, got time.Time)
	}{
		{
			name:    "day-prefixed format",
			input:   "Mon 2026-01-29 10:00:00 UTC",
			wantErr: false,
			check: func(t *testing.T, got time.Time) {
				if got.Year() != 2026 || got.Month() != time.January || got.Day() != 29 {
					t.Errorf("wrong date: got %v", got)
				}
				if got.Hour() != 10 || got.Minute() != 0 || got.Second() != 0 {
					t.Errorf("wrong time: got %v", got)
				}
			},
		},
		{
			name:    "without day prefix",
			input:   "2026-01-29 10:00:00 UTC",
			wantErr: false,
			check: func(t *testing.T, got time.Time) {
				if got.Year() != 2026 || got.Month() != time.January || got.Day() != 29 {
					t.Errorf("wrong date: got %v", got)
				}
			},
		},
		{
			name:    "different day and timezone",
			input:   "Fri 2025-12-05 14:30:45 EST",
			wantErr: false,
			check: func(t *testing.T, got time.Time) {
				if got.Year() != 2025 || got.Month() != time.December || got.Day() != 5 {
					t.Errorf("wrong date: got %v", got)
				}
				if got.Hour() != 14 || got.Minute() != 30 || got.Second() != 45 {
					t.Errorf("wrong time: got %v", got)
				}
			},
		},
		{
			name:    "empty string returns error",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid format returns error",
			input:   "not-a-timestamp",
			wantErr: true,
		},
		{
			name:    "ISO format not supported",
			input:   "2026-01-29T10:00:00Z",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSystemdTimestamp(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseSystemdTimestamp(%q) expected error, got nil (time: %v)", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSystemdTimestamp(%q) unexpected error: %v", tt.input, err)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestDirSize(t *testing.T) {
	t.Run("directory with known-size files", func(t *testing.T) {
		dir := t.TempDir()

		// Create files with known sizes
		if err := os.WriteFile(filepath.Join(dir, "file1.txt"), make([]byte, 100), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file2.txt"), make([]byte, 200), 0644); err != nil {
			t.Fatal(err)
		}

		// Create a subdirectory with a file
		subDir := filepath.Join(dir, "subdir")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(subDir, "file3.txt"), make([]byte, 300), 0644); err != nil {
			t.Fatal(err)
		}

		got := dirSize(dir)
		want := int64(600)
		if got != want {
			t.Errorf("dirSize() = %d, want %d", got, want)
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()

		got := dirSize(dir)
		if got != 0 {
			t.Errorf("dirSize() on empty dir = %d, want 0", got)
		}
	})

	t.Run("non-existent directory", func(t *testing.T) {
		got := dirSize("/nonexistent/path/that/does/not/exist")
		if got != 0 {
			t.Errorf("dirSize() on non-existent dir = %d, want 0", got)
		}
	})

	t.Run("single file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "only.txt"), make([]byte, 512), 0644); err != nil {
			t.Fatal(err)
		}

		got := dirSize(dir)
		want := int64(512)
		if got != want {
			t.Errorf("dirSize() = %d, want %d", got, want)
		}
	})
}
