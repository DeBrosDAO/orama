package installers

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	resolvedDropInDir        = "/etc/systemd/resolved.conf.d"
	resolvedStubDropInFile   = "no-stub.conf"
	resolvedMulticastDropIn  = "10-orama-no-multicast.conf"
	resolvedMulticastContent = "[Resolve]\nLLMNR=no\nMulticastDNS=no\n"
)

// writeResolvedDropIn writes a systemd-resolved drop-in and reports whether the
// file's content changed, so a caller restarts resolved only when it has to.
func writeResolvedDropIn(dir, name, content string) (bool, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("failed to create %s: %w", dir, err)
	}
	path := filepath.Join(dir, name)
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, []byte(content)) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return false, fmt.Errorf("failed to write resolved config %s: %w", path, err)
	}
	return true, nil
}

// DisableResolvedMulticast turns off LLMNR and mDNS in systemd-resolved on any
// node that runs it. resolved answers LLMNR on 0.0.0.0:5355 and mDNS on 5353 by
// default (Ubuntu and Debian ship LLMNR on): a public-address listener no Orama
// service uses, since names resolve through CoreDNS and the overlay, never
// through multicast. Unlike the stub listener this is not nameserver-only.
func DisableResolvedMulticast() error {
	if err := exec.Command("systemctl", "is-active", "--quiet", "systemd-resolved").Run(); err != nil {
		return nil // Not running, nothing to do
	}
	changed, err := writeResolvedDropIn(resolvedDropInDir, resolvedMulticastDropIn, resolvedMulticastContent)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if output, err := exec.Command("systemctl", "restart", "systemd-resolved").CombinedOutput(); err != nil {
		return fmt.Errorf("restart systemd-resolved after turning off LLMNR and mDNS: %w\n%s", err, output)
	}
	return nil
}
