package node

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/pushcmd"
)

// pushCmd is `orama node push`, the same command as the top-level `orama push`.
var pushCmd = pushcmd.NewCmd("push")
