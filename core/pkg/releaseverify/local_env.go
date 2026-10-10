//go:build localrepo

package releaseverify

import "os"

// localAllowedByEnv is true when the process asks for local repositories with AllowLocalEnv. It
// exists only in a binary built with the localrepo tag, which a release build never has.
func localAllowedByEnv() bool { return os.Getenv(AllowLocalEnv) == "1" }
