package deployments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"go.uber.org/zap"
)

// A deployment's instance names things on the host, not in a registry: its
// unit, its directory <base>/<instance>, its staged environment and token
// files. Every gateway on a host shares them, but each reads only its own
// deployments table — the index gateway and every namespace gateway have their
// own — so CheckNewDeploymentName cannot see a colliding deployment made
// through another gateway, and its SELECT is not atomic with the INSERT after
// it.
//
// So the host arbitrates. A deployment claims its instance by creating the
// directory with os.Mkdir, which exactly one caller wins, and recording its
// (namespace, name) in an owner marker inside it. Whoever finds the directory
// already there reads the marker: the same owner carries on (an update, a
// replica, a retried create), anyone else is refused.

const (
	// ownerMarkerName is the file in a deployment directory that names its
	// owner. Extraction never writes it (tarExtractArgs), so an archive cannot
	// change who owns the directory it is unpacked into.
	ownerMarkerName = ".orama-owner"

	// ownerMarkerMaxBytes bounds a marker read: it is two short names in JSON.
	ownerMarkerMaxBytes = 4096

	// deployBaseDirMode is the mode of the directory holding every
	// deployment: only the gateway (and root) can reach into it. A deployment's
	// unit does not go through it — systemd binds the unit's own directory into
	// the unit's mount namespace, where the rest of /opt/orama is an empty
	// tmpfs — so no other user on the host can reach any tenant's files.
	deployBaseDirMode fs.FileMode = 0o700

	// deployDirMode is one deployment directory's mode. It stays readable by
	// others because its reader is the deployment's own unit, running as a
	// dynamic user that shares no group with the gateway, and a bind mount
	// keeps the directory's mode. Only that unit can reach it: the base
	// directory above is 0700, and no other unit has it in its namespace.
	deployDirMode fs.FileMode = 0o755

	// ownerMarkerMode lets the unit read the marker; only the gateway writes it.
	ownerMarkerMode fs.FileMode = 0o644
)

// instanceClaim is a deployment's hold on its instance on this host.
type instanceClaim struct {
	dir string
	// created is true when this request created the directory. Only then is
	// removing it on failure this request's to do.
	created bool
}

// claimInstance claims namespace/name's instance on this host. It runs before
// any of the deployment's files are written, and leaves its directory in
// place: created and marked, or found already owned by namespace/name.
//
// An *instanceTakenError means another deployment holds the instance here.
func (s *DeploymentService) claimInstance(ctx context.Context, baseDeployPath, namespace, name string) (*instanceClaim, error) {
	dir := process.DeployDir(baseDeployPath, namespace, name)
	if err := ensureDeployBase(baseDeployPath); err != nil {
		return nil, err
	}

	err := os.Mkdir(dir, deployDirMode)
	switch {
	case err == nil:
		markErr := writeOwnerMarker(dir, namespace, name, true)
		if markErr == nil {
			return &instanceClaim{dir: dir, created: true}, nil
		}
		if !errors.Is(markErr, fs.ErrExist) {
			// Not a recursive removal: if the directory is no longer the
			// empty one just made, it is not this request's to delete.
			if rmErr := os.Remove(dir); rmErr != nil {
				return nil, fmt.Errorf("mark deployment directory %s: %w (and removing it again failed: %v)", dir, markErr, rmErr)
			}
			return nil, fmt.Errorf("mark deployment directory %s: %w", dir, markErr)
		}
		// A marker appeared between the Mkdir and the write: an update of the
		// instance's owner swapped its staged directory in. It decides.
	case !errors.Is(err, fs.ErrExist):
		return nil, fmt.Errorf("create deployment directory %s: %w", dir, err)
	}

	if err := s.checkInstanceOwner(ctx, dir, namespace, name); err != nil {
		return nil, err
	}
	return &instanceClaim{dir: dir}, nil
}

// ensureDeployBase creates the deployments directory, or narrows an existing
// one: nodes before this release created it 0755, and MkdirAll leaves an
// existing directory's mode alone.
func ensureDeployBase(base string) error {
	if err := os.MkdirAll(base, deployBaseDirMode); err != nil {
		return fmt.Errorf("create the deployments directory %s: %w", base, err)
	}
	if err := os.Chmod(base, deployBaseDirMode); err != nil {
		return fmt.Errorf("restrict the deployments directory %s to %v: %w", base, deployBaseDirMode, err)
	}
	return nil
}

// errInstanceNotNew refuses a create whose instance directory already exists
// and is this deployment's own: the deployment exists, another create of it is
// in flight, or a failed one left files. Proceeding would extract into files
// another request owns.
var errInstanceNotNew = errors.New("this deployment already has files on this node: " +
	"it exists (deploy with --update), another create of it is in progress, " +
	"or an earlier one left files behind (delete the deployment first)")

// claimNewInstance claims the instance for a create, which must be the request
// that makes its directory. Two creates of the same deployment at once used to
// both pass the owner check and extract into one directory. A directory that
// already exists is never adopted here — not even one without a marker, which
// may be a create that has made it and not yet marked it: a different owner's
// marker is a collision, anything else is errInstanceNotNew.
func (s *DeploymentService) claimNewInstance(ctx context.Context, baseDeployPath, namespace, name string) (*instanceClaim, error) {
	dir := process.DeployDir(baseDeployPath, namespace, name)
	if err := ensureDeployBase(baseDeployPath); err != nil {
		return nil, err
	}
	err := os.Mkdir(dir, deployDirMode)
	if err == nil {
		if markErr := writeOwnerMarker(dir, namespace, name, true); markErr != nil {
			if rmErr := os.Remove(dir); rmErr != nil {
				return nil, fmt.Errorf("mark deployment directory %s: %w (and removing it again failed: %v)", dir, markErr, rmErr)
			}
			return nil, fmt.Errorf("mark deployment directory %s: %w", dir, markErr)
		}
		return &instanceClaim{dir: dir, created: true}, nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("create deployment directory %s: %w", dir, err)
	}
	owner, err := readOwnerMarker(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, errInstanceNotNew
	case err != nil:
		return nil, err
	}
	if err := s.compareOwner(owner, namespace, name); err != nil {
		return nil, err
	}
	return nil, errInstanceNotNew
}

// release undoes a claim this request made, removing the directory and
// whatever was extracted into it. A directory that already existed is left
// alone: it was not this request's.
func (c *instanceClaim) release() error {
	if c == nil || !c.created {
		return nil
	}
	if err := os.RemoveAll(c.dir); err != nil {
		return fmt.Errorf("remove deployment directory %s: %w", c.dir, err)
	}
	return nil
}

// releaseClaim releases a claim on a request that already failed. The failure
// being reported is the request's; a directory left behind is logged as the
// error it is, because it keeps the name unusable until it is removed.
func (s *DeploymentService) releaseClaim(claim *instanceClaim) {
	if err := claim.release(); err != nil {
		s.logger.Error("A failed deployment left its directory behind; remove it before the name can be used again",
			zap.Error(err))
	}
}

// checkInstanceOwner decides whether the existing deployment directory dir is
// namespace/name's.
//
// A directory without a marker predates the claim. It is adopted by
// namespace/name only if this gateway's registry has that deployment — the
// directory's name maps to it, and the row says it exists — and refused
// otherwise: it may be another gateway's deployment, and nothing here can tell.
func (s *DeploymentService) checkInstanceOwner(ctx context.Context, dir, namespace, name string) error {
	owner, err := readOwnerMarker(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return s.adoptUnmarkedDir(ctx, dir, namespace, name)
	}
	if err != nil {
		return err
	}
	return s.compareOwner(owner, namespace, name)
}

// adoptUnmarkedDir marks a pre-claim directory as namespace/name's when this
// gateway's registry has that deployment.
func (s *DeploymentService) adoptUnmarkedDir(ctx context.Context, dir, namespace, name string) error {
	instance := process.InstanceName(namespace, name)
	registered, err := s.hasDeploymentRow(ctx, namespace, name)
	if err != nil {
		return err
	}
	if !registered {
		s.logger.Warn("Refused a deployment: its directory exists with no owner and this registry has no such deployment",
			zap.String("instance", instance), zap.String("dir", dir),
			zap.String("namespace", namespace), zap.String("name", name))
		return &instanceTakenError{instance: instance, unowned: true}
	}
	err = writeOwnerMarker(dir, namespace, name, true)
	if errors.Is(err, fs.ErrExist) {
		// Marked concurrently; the marker now decides.
		owner, readErr := readOwnerMarker(dir)
		if readErr != nil {
			return readErr
		}
		return s.compareOwner(owner, namespace, name)
	}
	if err != nil {
		return fmt.Errorf("adopt deployment directory %s: %w", dir, err)
	}
	s.logger.Info("Adopted a deployment directory that predates owner markers",
		zap.String("instance", instance), zap.String("namespace", namespace), zap.String("name", name))
	return nil
}

func (s *DeploymentService) compareOwner(owner instanceOwner, namespace, name string) error {
	if owner.Namespace == namespace && owner.Name == name {
		return nil
	}
	instance := process.InstanceName(namespace, name)
	s.logger.Warn("Refused a deployment whose unit instance another deployment holds on this host",
		zap.String("instance", instance),
		zap.String("namespace", namespace), zap.String("name", name),
		zap.String("owner_namespace", owner.Namespace), zap.String("owner_name", owner.Name))
	return &instanceTakenError{instance: instance}
}

// ownsInstanceDir reports whether the deployment directory dir may be treated
// as namespace/name's when tearing it down: it is theirs, or it is not there.
// A directory another deployment holds is not, and neither is its unit.
func (s *DeploymentService) ownsInstanceDir(ctx context.Context, dir, namespace, name string) (bool, error) {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return true, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect deployment directory %s: %w", dir, err)
	}
	err := s.checkInstanceOwner(ctx, dir, namespace, name)
	var taken *instanceTakenError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &taken):
		return false, nil
	default:
		return false, err
	}
}

// hasDeploymentRow reports whether this gateway's registry has namespace/name.
func (s *DeploymentService) hasDeploymentRow(ctx context.Context, namespace, name string) (bool, error) {
	var rows []instanceOwner
	if err := s.db.Query(ctx, &rows,
		`SELECT namespace, name FROM deployments WHERE namespace = ? AND name = ? LIMIT 1`,
		namespace, name); err != nil {
		return false, fmt.Errorf("look up deployment %s/%s: %w", namespace, name, err)
	}
	return len(rows) > 0, nil
}

// writeOwnerMarker records namespace/name as the owner of dir. The marker is
// written whole to a temporary file first, so it is never read half-written.
// exclusive fails with fs.ErrExist when dir already has a marker — that is
// the claim; otherwise an existing marker is replaced, for a staged directory
// about to be swapped in.
func writeOwnerMarker(dir, namespace, name string, exclusive bool) error {
	data, err := json.Marshal(instanceOwner{Namespace: namespace, Name: name})
	if err != nil {
		return fmt.Errorf("encode owner marker: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ownerMarkerName+".tmp-*")
	if err != nil {
		return fmt.Errorf("create owner marker in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr, os.Chmod(tmpPath, ownerMarkerMode)); err != nil {
		return errors.Join(fmt.Errorf("write owner marker %s: %w", tmpPath, err), os.Remove(tmpPath))
	}

	target := filepath.Join(dir, ownerMarkerName)
	if !exclusive {
		if err := os.Rename(tmpPath, target); err != nil {
			return errors.Join(fmt.Errorf("install owner marker %s: %w", target, err), os.Remove(tmpPath))
		}
		return nil
	}
	linkErr := os.Link(tmpPath, target)
	if err := os.Remove(tmpPath); err != nil {
		return errors.Join(linkErr, fmt.Errorf("remove temporary owner marker %s: %w", tmpPath, err))
	}
	if linkErr != nil {
		// os.Link reports an existing target as fs.ErrExist, which callers
		// test for.
		return fmt.Errorf("install owner marker %s: %w", target, linkErr)
	}
	return nil
}

// readOwnerMarker reads dir's owner. fs.ErrNotExist means dir has no marker.
// A marker that is a symlink, oversized or malformed is an error: something
// other than the gateway wrote it.
func readOwnerMarker(dir string) (instanceOwner, error) {
	path := filepath.Join(dir, ownerMarkerName)
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return instanceOwner{}, err
		}
		return instanceOwner{}, fmt.Errorf("open owner marker %s: %w", path, err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, ownerMarkerMaxBytes+1))
	if err != nil {
		return instanceOwner{}, fmt.Errorf("read owner marker %s: %w", path, err)
	}
	var owner instanceOwner
	if len(data) > ownerMarkerMaxBytes || json.Unmarshal(data, &owner) != nil || owner.Namespace == "" || owner.Name == "" {
		return instanceOwner{}, fmt.Errorf("owner marker %s is not one this gateway wrote; "+
			"an operator must check which deployment the directory belongs to", path)
	}
	return owner, nil
}

// tarExtractArgs is the tar invocation for unpacking a deployment archive
// into dest. The owner marker is excluded at any depth: the directory's owner
// is recorded by the gateway, never by the archive.
func tarExtractArgs(archive, dest string) []string {
	return []string{"--exclude=" + ownerMarkerName, "-xzf", archive, "-C", dest}
}
