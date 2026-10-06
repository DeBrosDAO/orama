package cosmovisor

import (
	"fmt"
	"os/user"
	"strconv"
)

// LookupAccount is the uid and primary gid of the account cosmovisor runs
// as. A missing account is an error: the global install creates it, and a
// layout it cannot move current in would halt the chain at the upgrade
// height instead of switching binaries.
func LookupAccount(account string) (uid, gid int, err error) {
	u, err := user.Lookup(account)
	if err != nil {
		return 0, 0, fmt.Errorf("look up the %s account that runs the chain: %w", account, err)
	}
	if uid, err = strconv.Atoi(u.Uid); err != nil {
		return 0, 0, fmt.Errorf("the %s account has a non-numeric uid %q: %w", account, u.Uid, err)
	}
	if gid, err = strconv.Atoi(u.Gid); err != nil {
		return 0, 0, fmt.Errorf("the %s account has a non-numeric gid %q: %w", account, u.Gid, err)
	}
	return uid, gid, nil
}
