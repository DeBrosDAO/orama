package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/pkg/encryption"
)

func main() {
	var outputPath string
	var displayOnly bool

	flag.StringVar(&outputPath, "output", "", "Output path for identity key")
	flag.BoolVar(&displayOnly, "display-only", false, "Only display identity info, don't save")
	flag.Parse()

	// Generate identity using shared package
	info, err := encryption.GenerateIdentity()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to generate identity: %v\n", err)
		os.Exit(1)
	}

	// If display only, just show the info
	if displayOnly {
		fmt.Printf("Node Identity: %s\n", info.PeerID.String())
		return
	}

	// Save to file using shared package
	if outputPath == "" {
		fmt.Fprintln(os.Stderr, "Output path is required")
		os.Exit(1)
	}

	if err := encryption.SaveIdentity(info, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save identity: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated Node Identity: %s\n", info.PeerID.String())
	fmt.Printf("Identity saved to: %s\n", outputPath)
}
