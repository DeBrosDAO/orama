package oramacli

import (
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// exactSecretFlags are flags whose value never reaches evidence or an error
// message. --key-file is not one: its value is a path, and the harness never
// reads the file.
var exactSecretFlags = map[string]bool{"--key": true, "--api-key": true, "--mnemonic": true}

// secretFlagPrefixes mask every flag named after them: --password,
// --password-file's neighbours, --token, --token-file, --secret, ...
var secretFlagPrefixes = []string{"--password", "--token", "--secret"}

func isSecretFlag(name string) bool {
	if exactSecretFlags[name] {
		return true
	}
	for _, p := range secretFlagPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// RedactArgs masks the values of secret flags, in both "--flag value" and
// "--flag=value" forms. A secret flag followed by another flag is a boolean
// (`--password` alone asks RootWallet): the next flag is kept.
func RedactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = a
		if name, _, ok := strings.Cut(a, "="); ok && strings.HasPrefix(name, "--") && isSecretFlag(name) {
			out[i] = name + "=" + secrets.Mask
			continue
		}
		if i > 0 && isSecretFlag(args[i-1]) && !strings.HasPrefix(a, "--") {
			out[i] = secrets.Mask
		}
	}
	return out
}
