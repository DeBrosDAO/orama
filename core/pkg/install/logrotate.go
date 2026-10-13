package install

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

// GenerateLogrotateConfig renders the rotation policy for the log files under
// <oramaDir>/logs.
//
// The host units install wrote before the namespace templates redirected their
// output there with `StandardOutput=append:<file>`, which never rotates on its
// own; node.log was observed at 2.7 GB, gateway.log at 861 MB. Every unit now
// logs to the journal, which bounds itself, but upgraded nodes still carry
// those files and anything written there since, so the rule stays.
//
// `copytruncate` rather than create-and-signal: a writer holding the file open
// has no reopen signal to send, so renaming the file would leave it writing to
// an unlinked inode. `su orama orama` runs the rotation as the user that owns
// the directory, so a symlink planted there is never followed by root.
func GenerateLogrotateConfig(oramaDir string) string {
	logGlob := filepath.Join(oramaDir, "logs", "*.log")
	return fmt.Sprintf(`# Managed by Orama — do not edit by hand.
#
# Files left by the pre-journal host units and anything that still writes here.
# copytruncate keeps an open writer's fd valid.
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
// Requires root.
func InstallLogrotateConfig(oramaDir string) error {
	cfg := GenerateLogrotateConfig(oramaDir)
	if err := os.WriteFile(logrotateConfigPath, []byte(cfg), logrotateFileMode); err != nil {
		return fmt.Errorf("failed to write %s: %w", logrotateConfigPath, err)
	}
	return nil
}
