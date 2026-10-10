//go:build !localrepo

package releaseverify

// localAllowedByEnv is false in every binary built without the localrepo tag: the environment
// cannot turn off the check that keeps the agent from reaching a loopback or private network.
func localAllowedByEnv() bool { return false }
