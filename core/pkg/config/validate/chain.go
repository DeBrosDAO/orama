package validate

import (
	"path/filepath"
	"regexp"
)

// keyFilePathRE is the characters a faucet key file path may use. The path is written into
// node.yaml between double quotes by the installer, so nothing that could end the string or the
// line is allowed in it.
var keyFilePathRE = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)

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
	case c.KeyFile != "" && (!filepath.IsAbs(c.KeyFile) || filepath.Clean(c.KeyFile) != c.KeyFile || !keyFilePathRE.MatchString(c.KeyFile)):
		return []error{ValidationError{
			Path:    "chain.faucet.key_file",
			Message: "must be a clean absolute path of letters, digits, '_', '.', '-' and '/'",
			Hint:    "for example /opt/orama/.orama/secrets/chain-faucet.key",
		}}
	}
	return nil
}
