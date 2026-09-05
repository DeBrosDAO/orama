package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/rolloutcmd"
)

// rolloutCmd is `orama node rollout`, the same command as `orama rollout`.
var rolloutCmd = rolloutcmd.NewCmd("rollout")
