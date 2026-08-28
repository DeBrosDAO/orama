package production

import (
	"strings"
	"testing"
)

func TestGenerateLogrotateConfig_coversServiceLogGlob(t *testing.T) {
	cfg := GenerateLogrotateConfig("/opt/orama/.orama")

	if !strings.Contains(cfg, "/opt/orama/.orama/logs/*.log {") {
		t.Errorf("config does not target the service log directory:\n%s", cfg)
	}
}

// systemd's append: redirection keeps the file descriptor open and offers no
// reopen signal, so a rename-based rotation would leave every service writing
// into an unlinked inode — the log would appear rotated while still growing
// invisibly. copytruncate is the only correct mode here.
func TestGenerateLogrotateConfig_usesCopytruncate(t *testing.T) {
	cfg := GenerateLogrotateConfig("/opt/orama/.orama")

	if !strings.Contains(cfg, "copytruncate") {
		t.Error("copytruncate is required for systemd append: logs; without it rotation silently does nothing")
	}
	if strings.Contains(cfg, "\n    create 0644 orama orama\n") && !strings.Contains(cfg, "copytruncate") {
		t.Error("create without copytruncate would orphan the open fd")
	}
}

func TestGenerateLogrotateConfig_boundsGrowth(t *testing.T) {
	cfg := GenerateLogrotateConfig("/opt/orama/.orama")

	for _, want := range []string{"daily", "rotate 7", "maxsize 200M", "compress"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q — growth is not bounded:\n%s", want, cfg)
		}
	}
}

func TestGenerateLogrotateConfig_toleratesMissingLogs(t *testing.T) {
	cfg := GenerateLogrotateConfig("/opt/orama/.orama")

	// A fresh node has no logs yet; logrotate must not error on the empty glob.
	if !strings.Contains(cfg, "missingok") {
		t.Error("missingok required so a fresh install does not produce logrotate errors")
	}
	if !strings.Contains(cfg, "notifempty") {
		t.Error("notifempty avoids rotating empty files")
	}
}

func TestGenerateLogrotateConfig_respectsCustomDir(t *testing.T) {
	cfg := GenerateLogrotateConfig("/var/lib/orama-test")

	if !strings.Contains(cfg, "/var/lib/orama-test/logs/*.log") {
		t.Errorf("config ignored the supplied orama dir:\n%s", cfg)
	}
}
