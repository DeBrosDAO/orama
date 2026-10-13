package main

import (
	"fmt"
	"os/user"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// verifyDeploymentDir refuses to let a deployment unit start unless dir, which
// PID 1 is about to bind and run from by name, is a real directory reached
// with no symlink below the anchor and owned by owner — the user the gateway
// that staged it runs as (privhelper.DeploymentDirToVerify says why).
func verifyDeploymentDir(root rootfs.Root, dir, owner string) error {
	uid, _, err := root.DirOwner(dir)
	if err != nil {
		return fmt.Errorf("the deployment directory is not one this helper will let systemd bind: %w", err)
	}
	u, err := user.Lookup(owner)
	if err != nil {
		return fmt.Errorf("look up the %s user: %w", owner, err)
	}
	if strconv.FormatUint(uint64(uid), 10) != u.Uid {
		return fmt.Errorf("the deployment directory %s is owned by uid %d, not %s", dir, uid, owner)
	}
	return nil
}
