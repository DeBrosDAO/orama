package cosmovisor

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

// ChownTo is a Layout.Chown that gives a directory to account and its
// primary group. A missing account is an error: the global install creates
// it, and a directory cosmovisor cannot write would halt the chain at the
// upgrade height instead of switching binaries.
func ChownTo(account string) func(path string) error {
	return func(path string) error {
		u, err := user.Lookup(account)
		if err != nil {
			return fmt.Errorf("look up the %s account that runs the chain: %w", account, err)
		}
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return fmt.Errorf("the %s account has a non-numeric uid %q: %w", account, u.Uid, err)
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			return fmt.Errorf("the %s account has a non-numeric gid %q: %w", account, u.Gid, err)
		}
		if err := os.Lchown(path, uid, gid); err != nil {
			return fmt.Errorf("chown %s to %s: %w", path, account, err)
		}
		return nil
	}
}
