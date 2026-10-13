package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// envSealedFD names the inherited pipe a re-executed runner reads its
// secrets from. The fd number is not secret.
const envSealedFD = "E2E_SEALED_FD"

// sealPipeMax bounds the sealed secrets: they are written into the pipe
// before the exec, so they must fit its buffer (16 KiB on macOS, 64 KiB on
// Linux) or the write would block forever.
const sealPipeMax = 16 << 10

// sealedCommands start feature processes: they run with the secret
// environment removed from their own environ (see sealProcess).
var sealedCommands = map[string]bool{"run": true, "test": true}

// sealProcess keeps the secret environment out of every environ a feature
// process can read. The first time through it re-executes the runner with
// the secret variables removed from its environment and their values in an
// inherited pipe; the re-executed runner reads them into memory
// (secrets.Seal) and closes the pipe. /proc/<pid>/environ (and ps -E) of the
// runner then shows none of them, and os.Environ(), which every child
// inherits, has none either. Code that needs a secret reads it with
// secrets.LookupEnv.
func sealProcess(command string) error {
	if !sealedCommands[command] {
		return nil
	}
	if fd, ok := os.LookupEnv(envSealedFD); ok {
		return loadSealed(fd)
	}
	clean, secret := secrets.SplitSecretEnv(os.Environ())
	if len(secret) == 0 {
		return nil
	}
	return reexecSealed(clean, secret)
}

// loadSealed reads the secrets from the inherited pipe fd and seals them.
func loadSealed(fdText string) error {
	fd, err := strconv.Atoi(fdText)
	if err != nil || fd <= int(os.Stderr.Fd()) {
		return fmt.Errorf("%s=%q is not an inherited pipe fd", envSealedFD, fdText)
	}
	f := os.NewFile(uintptr(fd), "sealed-secrets")
	values, rerr := secrets.ReadSealed(f)
	if err := errors.Join(rerr, f.Close()); err != nil {
		return fmt.Errorf("failed to read the sealed secrets from fd %d: %w", fd, err)
	}
	if err := os.Unsetenv(envSealedFD); err != nil {
		return fmt.Errorf("failed to remove %s from the environment: %w", envSealedFD, err)
	}
	return secrets.Seal(values)
}

// reexecSealed replaces this process with itself, run with clean as its
// environment and secret in a pipe; it returns only on failure.
func reexecSealed(clean []string, secret map[string]string) error {
	var buf bytes.Buffer
	if err := secrets.WriteSealed(&buf, secret); err != nil {
		return err
	}
	if buf.Len() > sealPipeMax {
		return fmt.Errorf("the secret environment is %d bytes, over the %d a sealing pipe holds", buf.Len(), sealPipeMax)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create the sealing pipe: %w", err)
	}
	_, werr := w.Write(buf.Bytes())
	if err := errors.Join(werr, w.Close()); err != nil {
		return errors.Join(fmt.Errorf("failed to fill the sealing pipe: %w", err), r.Close())
	}
	exe, err := os.Executable()
	if err != nil {
		return errors.Join(fmt.Errorf("failed to find the runner's executable to re-execute it: %w", err), r.Close())
	}
	fd := int(r.Fd())
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
		return errors.Join(fmt.Errorf("failed to let the re-executed runner inherit the sealing pipe: %w", err), r.Close())
	}
	env := append(clean, envSealedFD+"="+strconv.Itoa(fd))
	err = syscall.Exec(exe, os.Args, env)
	return errors.Join(fmt.Errorf("failed to re-execute %s without the secret environment: %w", exe, err), r.Close())
}
