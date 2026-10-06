package deployments

import (
	"context"
	"fmt"
	"time"
)

// deploymentKey names one deployment for the lock and the version counter.
func deploymentKey(namespace, name string) string { return namespace + "/" + name }

// deploymentLock is one deployment's lock and the number of callers holding or
// waiting for it. The entry is dropped when that reaches zero, so the table
// holds only deployments being changed now, not every one ever changed.
type deploymentLock struct {
	ch   chan struct{}
	refs int
}

// lockDeployment serializes the changes made to one deployment on this node: an
// environment change, an update, a rollback, a replica's setup and its teardown
// each read the deployment, change it and restart or remove it, and two of them
// interleaving leave the unit running a mix of both. It returns when the lock is
// held, or when ctx ends.
func (s *DeploymentService) lockDeployment(ctx context.Context, namespace, name string) (func(), error) {
	key := deploymentKey(namespace, name)
	s.deploymentLockMu.Lock()
	if s.deploymentLocks == nil {
		s.deploymentLocks = make(map[string]*deploymentLock)
	}
	lock := s.deploymentLocks[key]
	if lock == nil {
		lock = &deploymentLock{ch: make(chan struct{}, 1)}
		s.deploymentLocks[key] = lock
	}
	lock.refs++
	s.deploymentLockMu.Unlock()

	release := func() {
		s.deploymentLockMu.Lock()
		defer s.deploymentLockMu.Unlock()
		if lock.refs--; lock.refs == 0 {
			delete(s.deploymentLocks, key)
		}
	}
	select {
	case lock.ch <- struct{}{}:
		return func() { <-lock.ch; release() }, nil
	case <-ctx.Done():
		release()
		return nil, fmt.Errorf("another change to %s is in progress: %w", name, ctx.Err())
	}
}

// nextEnvVersion issues the version an environment change travels with: a
// timestamp, made strictly greater than the last one issued for the deployment
// and than recorded, so two changes in the same instant, or a clock that stepped
// back, still order. A replica refuses a version older than the one it applied.
// The home node of a deployment is fixed, so one clock issues them all. Call it
// with the deployment's lock held.
//
// recorded is the deployment row's updated_at: an environment change writes its
// version there, so after a restart (the in-memory last one is gone) with the
// clock behind, the next version still starts above the previous one instead of
// being refused by every replica for good. The floor is as exact as the
// database keeps the time; an update only ever raises it.
func (s *DeploymentService) nextEnvVersion(namespace, name string, recorded time.Time) int64 {
	s.envVersionMu.Lock()
	defer s.envVersionMu.Unlock()
	if s.envVersions == nil {
		s.envVersions = make(map[string]int64)
	}
	key := deploymentKey(namespace, name)
	version := time.Now().UnixNano()
	if last := s.envVersions[key]; version <= last {
		version = last + 1
	}
	if !recorded.IsZero() {
		if floor := recorded.UnixNano(); version <= floor {
			version = floor + 1
		}
	}
	s.envVersions[key] = version
	return version
}

// forgetDeployment drops what the service remembers of a deleted deployment.
func (s *DeploymentService) forgetDeployment(namespace, name string) {
	s.envVersionMu.Lock()
	defer s.envVersionMu.Unlock()
	delete(s.envVersions, deploymentKey(namespace, name))
}
