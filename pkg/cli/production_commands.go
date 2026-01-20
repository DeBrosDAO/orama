package cli

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production"
)

// HandleProdCommand handles production environment commands
func HandleProdCommand(args []string) {
	production.HandleCommand(args)
}
