package broker

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// reserve claims name for an add; it fails when the run already has a member
// or an eval cluster of that name, or another add is creating it.
func (s *Server) reserve(name string, isCluster bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending, s.extras, s.clusters = map[string]bool{}, map[string]fleet.Node{}, map[string]bool{}
	}
	_, isExtra := s.extras[name]
	if s.pending[name] || isExtra || s.clusters[name] || (!isCluster && s.inState(name)) {
		return fmt.Errorf("%s is already in use in run %s", name, s.State.RunID)
	}
	if err := s.checkServerCapLocked(); err != nil {
		return err
	}
	s.pending[name] = true
	return nil
}

// inState reports whether the loaded state has a member called name.
func (s *Server) inState(name string) bool {
	for _, list := range [][]fleet.Node{s.State.Nodes, s.State.Extras, s.State.Probes} {
		for _, n := range list {
			if n.Name == name {
				return true
			}
		}
	}
	return false
}

// stateCopy is the state plus every extra the broker holds, for one call.
func (s *Server) stateCopy() *fleet.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	own := *s.State
	own.Extras = append([]fleet.Node{}, s.State.Extras...)
	for _, n := range s.extras {
		own.Extras = append(own.Extras, n)
	}
	return &own
}

func (s *Server) addExtra(ctx context.Context, name, location string) (fleet.Node, error) {
	if err := s.reserve(name, false); err != nil {
		return fleet.Node{}, err
	}
	n, err := s.Cloud.AddExtra(ctx, s.stateCopy(), name, location)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, name)
	if err == nil {
		s.extras[name] = n
	}
	return n, err
}

// removeExtra deletes an extra the broker created or the state lists; a
// core node or a probe is refused.
func (s *Server) removeExtra(ctx context.Context, name string) error {
	s.mu.Lock()
	_, ours := s.extras[name]
	s.mu.Unlock()
	if !ours && !s.stateExtra(name) {
		return fmt.Errorf("refusing to remove %q: it is not an extra server of run %s", name, s.State.RunID)
	}
	if err := s.Cloud.RemoveExtra(ctx, s.stateCopy(), name); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.extras, name)
	s.mu.Unlock()
	return nil
}

func (s *Server) stateExtra(name string) bool {
	for _, n := range s.State.Extras {
		if n.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) addCluster(ctx context.Context, name string) (fleet.Cluster, error) {
	if err := s.reserve(name, true); err != nil {
		return fleet.Cluster{}, err
	}
	c, err := s.Cloud.AddCluster(ctx, s.stateCopy(), name)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, name)
	if err == nil {
		s.clusters[name] = true
	}
	return c, err
}

// removeCluster removes an eval cluster this broker installed.
func (s *Server) removeCluster(ctx context.Context, name string) error {
	s.mu.Lock()
	ours := s.clusters[name]
	s.mu.Unlock()
	if !ours {
		return fmt.Errorf("refusing to remove cluster %q: this runner did not install it", name)
	}
	if err := s.Cloud.RemoveCluster(ctx, s.stateCopy(), name); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.clusters, name)
	s.mu.Unlock()
	return nil
}
