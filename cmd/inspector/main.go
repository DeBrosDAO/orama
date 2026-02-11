package main

import (
	"os"

	"github.com/DeBrosOfficial/network/pkg/cli"
)

func main() {
	cli.HandleInspectCommand(os.Args[1:])
}
