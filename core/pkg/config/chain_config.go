package config

import "github.com/DeBrosOfficial/network/pkg/constants"

// ChainConfig is the chain block in node.yaml: what this node's gateway does for the chain beside
// it.
type ChainConfig struct {
	Faucet FaucetConfig `yaml:"faucet"`
}

// FaucetConfig is chain.faucet: the node's gateway serves POST /v1/chain/faucet, signing MsgFaucet
// with a key of its own for whoever asks. It is for a test network (a stagenet, devnet or
// localnet chain id; the gateway signs nowhere else) and is off unless an operator turns it on.
// `orama maint faucet init` creates the key and prints the account to fund.
type FaucetConfig struct {
	// Enabled turns the faucet on.
	Enabled bool `yaml:"enabled"`
	// KeyFile is the faucet key's file: owned by the gateway's account, mode 0600. Empty is
	// constants.ChainFaucetKeyFile.
	KeyFile string `yaml:"key_file,omitempty"`
}

// KeyFilePath is the key file the gateway is given: none when the faucet is off, else KeyFile or
// its default.
func (f FaucetConfig) KeyFilePath() string {
	switch {
	case !f.Enabled:
		return ""
	case f.KeyFile == "":
		return constants.ChainFaucetKeyFile
	}
	return f.KeyFile
}
