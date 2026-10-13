package broker

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
)

// Connection bounds; variables so tests can shorten them.
var (
	// requestReadTimeout is how long a connection has to send its request
	// line: a client that connects and says nothing cannot hold a slot.
	requestReadTimeout = 10 * time.Second
	// maxConns is how many connections are served at once; further ones
	// wait in the listen backlog.
	maxConns = 8
)

// Bounds on what feature processes can make the broker do.
const (
	// DefaultMaxServers caps the extras and eval clusters live or being
	// created at once when Server.MaxServers is zero: one feature's
	// allowance (manifest.MaxExtraNodes).
	DefaultMaxServers = manifest.MaxExtraNodes
	// maxTXTRecords caps the TXT records set through the broker and not
	// yet deleted.
	maxTXTRecords = 64
	// EnvMaxServers overrides the server cap (cmd/e2e-fleet reads it).
	EnvMaxServers = "E2E_BROKER_MAX_SERVERS"
)

// maxServers is the cap on live and pending servers.
func (s *Server) maxServers() int {
	if s.MaxServers > 0 {
		return s.MaxServers
	}
	return DefaultMaxServers
}

// checkServerCapLocked refuses one more server over the cap; s.mu is held.
func (s *Server) checkServerCapLocked() error {
	if n := len(s.extras) + len(s.clusters) + len(s.pending); n >= s.maxServers() {
		return fmt.Errorf("the broker already holds %d servers (extras, eval clusters and adds in flight), its cap is %d (%s)",
			n, s.maxServers(), EnvMaxServers)
	}
	return nil
}

// countTXT records a TXT set (+1) or delete of name; it refuses a set over
// the cap.
func (s *Server) countTXT(name string, set bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.txtNames == nil {
		s.txtNames = map[string]bool{}
	}
	if !set {
		delete(s.txtNames, name)
		return nil
	}
	if !s.txtNames[name] && len(s.txtNames) >= maxTXTRecords {
		return fmt.Errorf("the broker already holds %d TXT names, its cap is %d: delete some first", len(s.txtNames), maxTXTRecords)
	}
	s.txtNames[name] = true
	return nil
}
