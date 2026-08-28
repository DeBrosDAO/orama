package production

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// logrotateConfigPath is where the Orama log-rotation policy is installed.
	// Debian/Ubuntu's logrotate reads every file in this directory daily.
	logrotateConfigPath = "/etc/logrotate.d/orama"

	// logrotateFileMode matches what logrotate expects for its config files.
	logrotateFileMode = 0o644
)

// GenerateLogrotateConfig renders the rotation policy for the service logs
// written under <oramaDir>/logs.
//
// Every long-running unit is configured with `StandardOutput=append:<file>`,
// which never rotates on its own — the file grows for the lifetime of the
// install. On a busy node that reaches multiple gigabytes (node.log has been
// observed at 2.7 GB, gateway.log at 861 MB), and the only bound is the disk
// filling up and taking the node down with it.
//
// `copytruncate` is required rather than the default create-and-signal
// behaviour: systemd holds the append fd open and there is no reopen signal to
// send it, so renaming the file would leave the service writing to an unlinked
// inode and the "rotated" log would stay invisible and keep growing.
func GenerateLogrotateConfig(oramaDir string) string {
	logGlob := filepath.Join(oramaDir, "logs", "*.log")
	return fmt.Sprintf(`# Managed by Orama — do not edit by hand.
#
# Service units write with systemd's append: redirection, which holds the file
# descriptor open and never rotates. copytruncate keeps that fd valid.
%s {
    daily
    rotate 7
    maxsize 200M
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    su orama orama
    create 0644 orama orama
}
`, logGlob)
}

// InstallLogrotateConfig writes the rotation policy to logrotateConfigPath.
// Requires root. Returns an error the caller may downgrade to a warning —
// missing rotation degrades disk usage, it does not break the node.
func InstallLogrotateConfig(oramaDir string) error {
	cfg := GenerateLogrotateConfig(oramaDir)
	if err := os.WriteFile(logrotateConfigPath, []byte(cfg), logrotateFileMode); err != nil {
		return fmt.Errorf("failed to write %s: %w", logrotateConfigPath, err)
	}
	return nil
}
