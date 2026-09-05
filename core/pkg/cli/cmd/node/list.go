package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/nodescmd"
)

// listCmd is `orama node list`, the same inventory listing as `orama nodes`.
// The two spellings are one character apart, so the wrong guess used to give
// either a fleet listing or an unrelated command group. Both now list nodes.
var listCmd = nodescmd.NewListCmd("list")
