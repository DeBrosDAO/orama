//go:build !linux

package orchardproc

import "os/exec"

// StartsFromCheckedFile is false here: only Linux can start a process from an open file (through
// /proc). Elsewhere, which means a developer's machine, the file is started by name and a swap
// between the hash and the start is not prevented, so `oramad start` refuses such a host on every
// chain but a localnet or a scripted devnet (chain/app CheckNodeCanVerify).
const StartsFromCheckedFile = false

// configure leaves cmd starting the file by name.
func (b *pinnedBinary) configure(cmd *exec.Cmd) { cmd.Path = b.path }

// startCommand starts cmd.
func startCommand(cmd *exec.Cmd) error { return cmd.Start() }
