package deployments

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"go.uber.org/zap"
)

// A deployment's name decides its systemd unit, its directory and the files its
// secrets are staged in, all through process.InstanceName. Two (namespace, name)
// pairs can map to one instance — "a"/"b-c" and "a-b"/"c" are both "a-b-c" — and
// the second one to deploy would share the first one's unit, overwrite its
// files, and read its environment. The mapping stays (existing deployments'
// units and directories are named by it); a new deployment whose instance is
// already taken is refused before anything is written — here against the
// registry, and in instance_claim.go against the host.

// errInvalidDeploymentName marks a name ValidateName refused: a 400.
var errInvalidDeploymentName = errors.New("invalid deployment name")

// instanceTakenError is a name whose instance a deployment already has: a 409.
// It names the instance, never the other deployment — that one may belong to
// another tenant.
type instanceTakenError struct {
	instance string
	// exists is true when the holder is this very (namespace, name): the
	// deployment exists and the request should have been an update.
	exists bool
	// unowned is true when this node has a directory for the instance that
	// records no owner, and this gateway's registry has no deployment of it
	// (instance_claim.go).
	unowned bool
}

func (e *instanceTakenError) Error() string {
	switch {
	case e.exists:
		return "a deployment with this name already exists in this namespace; " +
			"deploy it again as an update (orama deploy ... --update) or choose a different name"
	case e.unowned:
		return fmt.Sprintf("deployment name conflict: this node already has files for the unit instance %q "+
			"that no deployment in this namespace owns; choose a different name, or ask an operator to "+
			"remove the leftover files", e.instance)
	}
	return fmt.Sprintf("deployment name conflict: this namespace and name map to the unit instance %q, "+
		"which another deployment already uses; choose a different name", e.instance)
}

// instanceOwner is a deployment that maps to a given instance: a registry row,
// or the owner marker in its directory on this host.
type instanceOwner struct {
	Namespace string `db:"namespace" json:"namespace"`
	Name      string `db:"name" json:"name"`
}

// CheckNewDeploymentName validates the name of a deployment about to be
// created and makes sure its instance is free.
//
// It has to run before the upload is extracted: the directory is named by the
// instance, so a conflicting deployment — or a second create of an existing
// one — would already have overwritten the owner's files by the time the
// registry insert failed on UNIQUE(namespace, name).
func (s *DeploymentService) CheckNewDeploymentName(ctx context.Context, namespace, name string) error {
	if err := process.ValidateName(name); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDeploymentName, err)
	}
	instance := process.InstanceName(namespace, name)

	// The expression is InstanceName in SQL. It scans the table, once per
	// create; creates are rare and the table is one row per deployment.
	var owners []instanceOwner
	err := s.db.Query(ctx, &owners,
		`SELECT namespace, name FROM deployments
		 WHERE REPLACE(namespace, '.', '-') || '-' || REPLACE(name, '.', '-') = ?`,
		instance)
	if err != nil {
		return fmt.Errorf("check whether unit instance %s is free: %w", instance, err)
	}
	if len(owners) == 0 {
		return nil
	}
	// The table may already hold a legacy collision; the request's own row,
	// if it is one of them, decides the answer.
	for _, o := range owners {
		if o.Namespace == namespace && o.Name == name {
			return &instanceTakenError{instance: instance, exists: true}
		}
	}
	s.logger.Warn("Refused a deployment whose unit instance another deployment has",
		zap.String("instance", instance),
		zap.String("namespace", namespace), zap.String("name", name),
		zap.String("owner_namespace", owners[0].Namespace), zap.String("owner_name", owners[0].Name))
	return &instanceTakenError{instance: instance}
}

// writeDeploymentNameError answers a CheckNewDeploymentName failure with the
// status it means.
func writeDeploymentNameError(w http.ResponseWriter, logger *zap.Logger, err error) {
	var taken *instanceTakenError
	switch {
	case errors.Is(err, errInvalidDeploymentName):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.As(err, &taken), errors.Is(err, errInstanceNotNew):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		logger.Error("Failed to check a deployment name", zap.Error(err))
		http.Error(w, "Failed to check the deployment name", http.StatusInternalServerError)
	}
}
