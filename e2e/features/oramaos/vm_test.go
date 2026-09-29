//go:build e2e_fleet

package oramaos

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

const (
	// EnvImage names a built OramaOS qcow2 (os/scripts/build.sh output).
	EnvImage = "E2E_ORAMAOS_IMAGE"
	// EnvFirmware overrides the UEFI firmware the image boots with
	// (systemd-boot needs UEFI; os/scripts/test-vm.sh uses OVMF).
	EnvFirmware     = "E2E_ORAMAOS_OVMF"
	defaultFirmware = "/usr/share/ovmf/OVMF.fd"
	qemuBin         = "qemu-system-x86_64"
	qemuImg         = "qemu-img"
	kvmDevice       = "/dev/kvm"
	// Guest ports (os/agent/internal/enroll/server.go, command/receiver.go).
	guestEnroll  = 9999
	guestCommand = 9998
	guestSSH     = 22
	// bootBudget covers a boot without KVM, under emulation.
	bootBudget = 15 * time.Minute
	feature    = "oramaos"
)

// codeLine is how the agent prints its registration code on the console.
var codeLine = regexp.MustCompile(`ENROLLMENT CODE: ([0-9a-f]+)`)

// vm is one OramaOS guest under QEMU, with its console captured and the
// agent's ports forwarded to loopback on the runner.
type vm struct {
	cmd                   *exec.Cmd
	mu                    sync.Mutex
	console               bytes.Buffer
	enroll, command, sshd int
	exited                chan struct{}
}

// bootVM boots the image the way os/scripts/test-vm.sh does (virtio disk
// and NIC, user networking with the agent's ports forwarded, serial on
// stdio), on a copy-on-write overlay so the image itself is never written.
func bootVM(t *testing.T) *vm {
	t.Helper()
	image, fw := requireImage(t)
	qemu := realistic.Tool(t, qemuBin, "the OramaOS lane boots the image under QEMU")
	img := realistic.Tool(t, qemuImg, "the OramaOS lane boots a copy-on-write overlay of the image")
	overlay := filepath.Join(t.TempDir(), "oramaos.qcow2")
	if out, err := exec.Command(img, "create", "-q", "-f", "qcow2", "-b", image, "-F", "qcow2", overlay).CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create an overlay of %s: %v %s", image, err, out)
	}
	v := &vm{exited: make(chan struct{})}
	ports := freePorts(t, 3)
	v.enroll, v.command, v.sshd = ports[0], ports[1], ports[2]
	v.cmd = exec.Command(qemu, v.args(overlay, fw)...)
	pr, pw := io.Pipe()
	v.cmd.Stdout, v.cmd.Stderr = pw, pw
	if err := v.cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", qemu, err)
	}
	go v.capture(pr)
	go func() { _ = v.cmd.Wait(); _ = pw.Close(); close(v.exited) }()
	t.Cleanup(func() { v.stop(t) })
	return v
}

func requireImage(t *testing.T) (string, string) {
	t.Helper()
	harness.Fleet(t)
	image := os.Getenv(EnvImage)
	if image == "" {
		harness.SkipNotApplicable(t, EnvImage+" is not set; build an image with os/scripts/build.sh and point it there to run the OramaOS lane")
	}
	if _, err := os.Stat(image); err != nil {
		t.Fatalf("%s=%s: %v", EnvImage, image, err)
	}
	fw := os.Getenv(EnvFirmware)
	if fw == "" {
		fw = defaultFirmware
	}
	if _, err := os.Stat(fw); err != nil {
		harness.SkipNotApplicable(t, "no UEFI firmware at "+fw+" (set "+EnvFirmware+"); the image boots with systemd-boot")
	}
	return image, fw
}

func (v *vm) args(disk, fw string) []string {
	accel := []string{"-cpu", "max"}
	if f, err := os.OpenFile(kvmDevice, os.O_RDWR, 0); err == nil {
		_ = f.Close()
		accel = []string{"-enable-kvm", "-cpu", "host"}
	}
	fwd := fmt.Sprintf("user,id=net0,hostfwd=tcp:127.0.0.1:%d-:%d,hostfwd=tcp:127.0.0.1:%d-:%d,hostfwd=tcp:127.0.0.1:%d-:%d",
		v.enroll, guestEnroll, v.command, guestCommand, v.sshd, guestSSH)
	return append(accel, "-smp", "2", "-m", "2G", "-bios", fw,
		"-drive", "file="+disk+",format=qcow2,if=virtio",
		"-netdev", fwd, "-device", "virtio-net-pci,netdev=net0",
		"-nographic", "-serial", "stdio", "-monitor", "none")
}

func (v *vm) capture(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		v.mu.Lock()
		v.console.Write(buf[:n])
		v.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// Console is everything the guest printed so far.
func (v *vm) Console() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.console.String()
}

// waitConsole waits until the console matches re and returns the match.
func (v *vm) waitConsole(t *testing.T, re *regexp.Regexp, what string) []string {
	t.Helper()
	var m []string
	eventually.Require(t, 2*time.Second, bootBudget, what, func() (bool, error) {
		select {
		case <-v.exited:
			return false, eventually.Stop(fmt.Errorf("QEMU exited:\n%s", realistic.Tail(v.Console())))
		default:
		}
		m = re.FindStringSubmatch(v.Console())
		return m != nil, nil
	})
	return m
}

// stop kills QEMU, waits for it and keeps the console (the registration
// code masked) as an artifact.
func (v *vm) stop(t *testing.T) {
	_ = v.cmd.Process.Kill()
	<-v.exited
	log := codeLine.ReplaceAllString(v.Console(), "ENROLLMENT CODE: [masked]")
	realistic.WriteText(t, harness.Fleet(t), feature, strings.ReplaceAll(t.Name(), "/", "_")+"-console.log", log)
}

// freePorts are loopback ports free right now.
func freePorts(t *testing.T, n int) []int {
	t.Helper()
	var out []int
	var ls []net.Listener
	for range n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ls = append(ls, l)
		out = append(out, l.Addr().(*net.TCPAddr).Port)
	}
	for _, l := range ls {
		_ = l.Close()
	}
	return out
}
