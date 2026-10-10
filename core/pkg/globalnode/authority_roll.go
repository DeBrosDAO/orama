package globalnode

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// CheckAuthorityRoll is the default Lifecycle.CheckAuthorityRoll on this node:
// it reads the installed network file and the consensus this authority holds.
func CheckAuthorityRoll() error {
	n, err := tornet.Load(filepath.Join(constants.GlobalStateRoot, constants.GlobalTorAuthoritiesFile))
	if err != nil {
		return fmt.Errorf("cannot tell whether the other authorities are voting: %w", err)
	}
	return checkAuthorityRoll(n, constants.GlobalTorDirauthHome, time.Now())
}

// checkAuthorityRoll refuses to take this directory authority down while
// another one has just started. An authority casts no Running vote for
// tornet.AuthorityLearnWindow after it starts and a consensus needs the flag in
// two of three votes, so two authorities restarted within that window leave
// the network without a consensus until the first has learned again (stagenet,
// 2026-10-10: three authorities restarted within four minutes made no
// consensus for 02:30 and 03:00, and every node's expired).
func checkAuthorityRoll(n tornet.Network, home string, now time.Time) error {
	info, err := tornet.ReadNodeInfo(home, now)
	if err != nil {
		return fmt.Errorf("cannot tell whether the other authorities are voting: %w", err)
	}
	if info.Fingerprint == "" {
		return errors.New("cannot tell whether the other authorities are voting: this authority has no fingerprint yet")
	}
	c, err := tornet.ReadHeldConsensus(home)
	if err != nil {
		return fmt.Errorf("cannot tell whether the other authorities are voting: %w", err)
	}
	if c == nil || !c.Valid(now) {
		return errors.New("cannot tell whether the other authorities are voting: this authority holds no valid consensus")
	}
	if c.Flavor != "ns" {
		return fmt.Errorf("cannot tell whether the other authorities are voting: the %s consensus has no publication times", c.Flavor)
	}
	if recent := tornet.RecentlyStartedAuthorities(n, *c, info.Fingerprint, now); len(recent) > 0 {
		return fmt.Errorf("not now, or the network is left without a consensus (an authority casts no Running vote for %s after it starts, and a consensus needs two of the three): %s",
			tornet.AuthorityLearnWindow, strings.Join(recent, "; "))
	}
	return nil
}
