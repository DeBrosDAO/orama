package enroll

import (
	"flag"
	"fmt"
	"os"
)

// Flags holds the parsed command-line flags for the enroll command.
type Flags struct {
	NodeIP     string // Public IP of the OramaOS node
	Code       string // Registration code (optional — fetched automatically if not provided)
	Token      string // Invite token for cluster joining
	GatewayURL string // Gateway HTTPS URL
	Env        string // Environment name (for display only)
}

// ParseFlags parses the enroll command flags.
func ParseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}

	fs.StringVar(&flags.NodeIP, "node-ip", "", "Public IP of the OramaOS node (required)")
	fs.StringVar(&flags.Code, "code", "", "Registration code from the node (auto-fetched if not provided)")
	fs.StringVar(&flags.Token, "token", "", "Invite token for cluster joining (required)")
	fs.StringVar(&flags.GatewayURL, "gateway", "", "Gateway URL (required, e.g. https://gateway.example.com)")
	fs.StringVar(&flags.Env, "env", "production", "Environment name")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if flags.NodeIP == "" {
		return nil, fmt.Errorf("--node-ip is required")
	}
	if flags.Token == "" {
		return nil, fmt.Errorf("--token is required")
	}
	if flags.GatewayURL == "" {
		return nil, fmt.Errorf("--gateway is required")
	}

	return flags, nil
}
