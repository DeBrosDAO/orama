package validate

import "path/filepath"

// FaucetConfig is the node.yaml chain.faucet block for validation purposes.
type FaucetConfig struct {
	Enabled bool
	KeyFile string
}

// ValidateFaucet checks chain.faucet. A key file with the faucet off is a mistake worth saying
// so: the operator expects a faucet that is not there. A relative path would resolve against
// whatever directory the gateway runs in.
func ValidateFaucet(c FaucetConfig) []error {
	switch {
	case !c.Enabled && c.KeyFile != "":
		return []error{ValidationError{
			Path:    "chain.faucet.key_file",
			Message: "is set but chain.faucet.enabled is false, so no faucet is served",
			Hint:    "set chain.faucet.enabled to true, or remove key_file",
		}}
	case c.KeyFile != "" && (!filepath.IsAbs(c.KeyFile) || filepath.Clean(c.KeyFile) != c.KeyFile):
		return []error{ValidationError{
			Path:    "chain.faucet.key_file",
			Message: "must be a clean absolute path",
			Hint:    "for example /opt/orama/.orama/secrets/chain-faucet.key",
		}}
	}
	return nil
}
