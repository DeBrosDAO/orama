package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/pushcmd"
)

// pushCmd is `orama node push`, the same command as the top-level `orama push`.
var pushCmd = pushcmd.NewCmd("push")
