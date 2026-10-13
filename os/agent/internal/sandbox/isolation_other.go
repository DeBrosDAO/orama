//go:build !linux

package sandbox

import "syscall"

// isolationAttrs on anything that is not Linux drops the namespace flags,
// which exist only there.
//
// OramaOS is Linux and the Makefile builds the agent with GOOS=linux, so this
// is never what ships. It exists so the module compiles — and therefore can be
// tested — on a developer's machine: without it every package that reaches
// sandbox, which is most of the agent, could not even be type-checked outside
// Linux, and none of it was.
func isolationAttrs(uid, gid uint32) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uid, Gid: gid},
	}
}
