//go:build unix

package cli

import (
	"fmt"
	"os"
	"syscall"
)

// lockEnvironmentConfig takes the exclusive lock every process shares around
// reading, changing and writing back environments.json, and waits for a holder
// to finish. The lock file is beside the config file and is not the config
// file, because the config is replaced by rename and a lock on a replaced file
// locks nothing. The kernel drops the lock if the holder dies.
func lockEnvironmentConfig(configPath string) (func() error, error) {
	path := configPath + environmentLockSuffix
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, environmentFilePerm)
	if err != nil {
		return nil, fmt.Errorf("open the environment lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f.Close, nil
}
