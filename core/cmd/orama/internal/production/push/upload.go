package push

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// uploadDirTemplate is the private directory an archive is uploaded into
	// on a node: a fixed /tmp path could be planted or swapped by another
	// local user before root reads it.
	uploadDirTemplate = "/tmp/orama-push.XXXXXXXX"
	// uploadName is the archive's name inside that directory.
	uploadName = "archive.tar.gz"
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
