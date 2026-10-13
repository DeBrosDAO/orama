//go:build linux

package orchardproc

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// StartsFromCheckedFile reports that this platform starts the verifier from the file whose hash
// was checked. Chains other than localnets and scripted devnets refuse to start without it
// (chain/app CheckNodeCanVerify).
const StartsFromCheckedFile = true

// configure makes cmd start the open, hashed file itself, through /proc, which is how fexecve is
// built on Linux. The file is passed to the child as descriptor 3 and the path is looked up there,
// after the fork, so no name on disk is resolved a second time and a swap of the file at the path
// after the hash changes nothing. (Passing it as an extra file rather than relying on the parent's
// close-on-exec descriptor also lets a script run: its interpreter opens the path after the exec.)
// A writer with write permission on the file's own inode could still change its bytes between the
// hash and the start; the installer makes the binary root's and read-only for the chain account.
//
// The child is killed when the node dies (Pdeathsig), besides ending on EOF of its stdin. Pdeathsig
// is tied to the thread that forked the child, and the Go runtime retires threads, which would kill
// a healthy verifier and halt the node: startCommand therefore forks from a thread that never exits.
func (b *pinnedBinary) configure(cmd *exec.Cmd) {
	cmd.Path = "/proc/self/fd/3"
	cmd.ExtraFiles = append(cmd.ExtraFiles, b.file)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

type startRequest struct {
	cmd  *exec.Cmd
	done chan error
}

var (
	spawnerOnce sync.Once
	spawnerCh   chan startRequest
)

// startCommand starts cmd from a goroutine locked to its OS thread for the life of the process. The
// goroutine never returns, so the thread never exits, and a child started from it keeps its
// Pdeathsig for as long as the node lives and for no longer.
func startCommand(cmd *exec.Cmd) error {
	spawnerOnce.Do(func() {
		spawnerCh = make(chan startRequest)
		go func() {
			runtime.LockOSThread()
			for r := range spawnerCh {
				r.done <- r.cmd.Start()
			}
		}()
	})
	done := make(chan error, 1)
	spawnerCh <- startRequest{cmd: cmd, done: done}
	return <-done
}
