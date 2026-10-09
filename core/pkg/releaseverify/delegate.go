package releaseverify

import (
	"fmt"
	"slices"
	"sort"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/trustedmetadata"
)

// verifyDelegated checks each delegated role the caller fetched against the
// delegation in the top-level targets, then adds its targets to out.
//
// A channel is a delegated role: "stable" is trusted for stable/*, "nightly"
// for nightly/*, each with keys and a threshold of its own, so a role cannot
// vouch for a path outside its channel. A role the targets metadata does not
// delegate is refused, as is a target outside the role's paths or one that
// another role already listed: the first reading of a path is the only one.
func verifyDelegated(trusted *trustedmetadata.TrustedMetadata, top *metadata.Metadata[metadata.TargetsType], fetched map[string][]byte, out map[string]Target) error {
	roles := make([]string, 0, len(fetched))
	for name := range fetched {
		roles = append(roles, name)
	}
	sort.Strings(roles)
	for _, name := range roles {
		delegation, ok := delegationFor(top, name)
		if !ok {
			return fmt.Errorf("%w: targets metadata does not delegate the role %q", ErrTargetPath, name)
		}
		loaded, err := trusted.UpdateDelegatedTargets(fetched[name], name, metadata.TARGETS)
		if err != nil {
			return roleError(name, err)
		}
		if err := addDelegatedTargets(delegation, loaded, out); err != nil {
			return err
		}
	}
	return nil
}

// maxRoleNameLen bounds a delegated role's name.
const maxRoleNameLen = 32

// ValidRoleName refuses a role name that is not a plain lowercase word. A
// role name becomes a file name in the metadata directory and a path in the
// repository, so it must not be able to name another file. A release channel is
// a delegated role, so a channel name is judged by this rule where the policy
// is written (updatepolicy.ValidChannel) and where it is verified.
func ValidRoleName(name string) error {
	if name == "" || len(name) > maxRoleNameLen {
		return fmt.Errorf("role name %q is empty or longer than %d characters", name, maxRoleNameLen)
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("role name %q has a character outside a-z, 0-9 and -", name)
		}
	}
	switch name {
	case metadata.ROOT, metadata.TIMESTAMP, metadata.SNAPSHOT, metadata.TARGETS:
		return fmt.Errorf("role name %q is a top-level role, not a delegated one", name)
	}
	return nil
}

// delegationFor is the delegation of role name in the top-level targets.
func delegationFor(top *metadata.Metadata[metadata.TargetsType], name string) (metadata.DelegatedRole, bool) {
	if top.Signed.Delegations == nil {
		return metadata.DelegatedRole{}, false
	}
	i := slices.IndexFunc(top.Signed.Delegations.Roles, func(r metadata.DelegatedRole) bool { return r.Name == name })
	if i < 0 {
		return metadata.DelegatedRole{}, false
	}
	return top.Signed.Delegations.Roles[i], true
}

// addDelegatedTargets adds the targets role lists to out, each checked
// against the paths role is delegated.
func addDelegatedTargets(role metadata.DelegatedRole, loaded *metadata.Metadata[metadata.TargetsType], out map[string]Target) error {
	for path, info := range loaded.Signed.Targets {
		in, err := role.IsDelegatedPath(path)
		if err != nil || !in {
			return fmt.Errorf("%w: role %q lists %q, which its delegation does not give it", ErrTargetPath, role.Name, path)
		}
		if _, dup := out[path]; dup {
			return fmt.Errorf("%w: %q is listed by two roles", ErrTargetPath, path)
		}
		target, err := targetFrom(path, role.Name, info)
		if err != nil {
			return err
		}
		out[path] = target
	}
	return nil
}
