//go:build !unix

package cosmovisor

import "errors"

// errUnix: staging needs O_NOFOLLOW and fd-relative calls; nodes run Linux.
var errUnix = errors.New("staging oramad needs a unix system")

// StageGenesis needs a unix system.
func (l Layout) StageGenesis(string, Verify) (string, error) { return "", errUnix }

// StageUpgrade needs a unix system.
func (l Layout) StageUpgrade(string, string, Verify) (string, error) { return "", errUnix }
