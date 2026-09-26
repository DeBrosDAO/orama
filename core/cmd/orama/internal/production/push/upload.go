package push

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

const (
	// uploadDirTemplate is the private directory an archive is uploaded into
	// on a node: a fixed /tmp path could be planted or swapped by another
	// local user before root reads it.
	uploadDirTemplate = "/tmp/orama-push.XXXXXXXX"
	// uploadName is the archive's name inside that directory.
	uploadName = "archive.tar.gz"
	// fanoutKeyDir holds, on the hub, the one key for each fanout target.
	fanoutKeyDir = "/dev/shm/.orama-fanout-keys"
	// fanoutSSHOptions authenticate the hub to a target with the one staged
	// key only.
	fanoutSSHOptions = "-o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes -o ConnectTimeout=10"
)

// uploadDirPattern is what mktemp returns for uploadDirTemplate; the directory
// goes into root shell commands, so nothing else passes.
var uploadDirPattern = regexp.MustCompile(`^/tmp/orama-push\.[A-Za-z0-9]{8}$`)

// makeUploadDir creates a private upload directory on a node through run,
// which executes a command there and returns its output.
func makeUploadDir(run func(cmd string) (string, error)) (string, error) {
	out, err := run("mktemp -d " + uploadDirTemplate)
	if err != nil {
		return "", fmt.Errorf("create an upload directory: %w", err)
	}
	dir := strings.TrimSpace(out)
	if !uploadDirPattern.MatchString(dir) {
		return "", fmt.Errorf("unexpected upload directory %q from mktemp", dir)
	}
	return dir, nil
}

// uploadPath is the archive inside an upload directory.
func uploadPath(dir string) string {
	return dir + "/" + uploadName
}

// sshVia is the command the hub runs to execute cmd on target with the
// target's staged key.
func sshVia(target inspector.Node, keyPath, cmd string) string {
	return fmt.Sprintf("ssh %s -i %s %s@%s '%s'", fanoutSSHOptions, keyPath, target.User, target.Host, cmd)
}
