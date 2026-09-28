package systemd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gatewaykeys"
)

// sharedUser is the uid every cluster unit runs as until isolation is applied.
// Install still creates only this user and still copies the static unit files
// unchanged. ServiceUser and RenderNamespaceUnit are not called from install.
const sharedUser = "orama"

// serviceIdentity is the unix account a namespace service runs as.
// isolated is used only when the isolation flag is on. ipfs-gc stays the ipfs
// user because it deletes blocks in that user's repository; it does not gain
// the gateway's key path. tor and ntfy already have their own accounts.
type serviceIdentity struct {
	shared   string
	isolated string
}

var serviceIdentities = map[string]serviceIdentity{
	string(ServiceTypeGateway):     {shared: sharedUser, isolated: "orama-gw"},
	string(ServiceTypeCaddy):       {shared: sharedUser, isolated: "orama-caddy"},
	string(ServiceTypeCoreDNS):     {shared: sharedUser, isolated: "orama-coredns"},
	string(ServiceTypeIPFS):        {shared: sharedUser, isolated: "orama-ipfs"},
	string(ServiceTypeIPFSGC):      {shared: sharedUser, isolated: "orama-ipfs"},
	string(ServiceTypeOlric):       {shared: sharedUser, isolated: "orama-olric"},
	string(ServiceTypeSFU):         {shared: sharedUser, isolated: "orama-sfu"},
	string(ServiceTypeIPFSCluster): {shared: sharedUser, isolated: "orama-ipfs-cluster"},
	string(ServiceTypePubsub):      {shared: sharedUser, isolated: "orama-pubsub"},
	string(ServiceTypeRQLite):      {shared: sharedUser, isolated: "orama-rqlite"},
	string(ServiceTypeTURN):        {shared: sharedUser, isolated: "orama-turn"},
	string(ServiceTypeSNIRouter):   {shared: sharedUser, isolated: "orama-sni"},
	string(ServiceTypeVault):       {shared: sharedUser, isolated: "orama-vault"},
	string(ServiceTypeNtfy):        {shared: "ntfy", isolated: "ntfy"},
	string(ServiceTypeTor):         {shared: "debian-tor", isolated: "debian-tor"},
	string(ServiceTypeWireGuard):   {shared: "root", isolated: "root"},
}

// ServiceUser maps a namespace service name to the unix account its unit runs
// as. isolate is the per-uid split. An unknown service is an error: falling
// back to the shared account would hand it every other unit's secrets.
func ServiceUser(service string, isolate bool) (string, error) {
	id, ok := serviceIdentities[service]
	if !ok {
		return "", fmt.Errorf("no unix user for service %q", service)
	}
	if isolate {
		return id.isolated, nil
	}
	return id.shared, nil
}

// RenderNamespaceUnit reads the shipped orama-namespace-<service>@.service
// template from dir. With isolate false it returns that file unchanged.
// With isolate true it sets User= and Group= from ServiceUser and, for the
// two units that share the secrets directory today, narrows what each may read.
func RenderNamespaceUnit(dir, service string, isolate bool) (string, error) {
	path := filepath.Join(dir, "orama-namespace-"+service+"@.service")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if !isolate {
		return string(data), nil
	}
	user, err := ServiceUser(service, true)
	if err != nil {
		return "", err
	}
	unit, err := rewriteUser(string(data), user)
	if err != nil {
		return "", fmt.Errorf("%s: %w", service, err)
	}
	switch service {
	case string(ServiceTypeGateway):
		unit, err = grantGatewayKeys(unit)
	case string(ServiceTypeIPFS):
		unit, err = isolateIPFSSecrets(unit)
	}
	if err != nil {
		return "", err
	}
	return unit, nil
}

func rewriteUser(unit, user string) (string, error) {
	lines := strings.Split(unit, "\n")
	userN, groupN := 0, 0
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "User="):
			lines[i] = "User=" + user
			userN++
		case strings.HasPrefix(line, "Group="):
			lines[i] = "Group=" + user
			groupN++
		}
	}
	if userN != 1 || groupN != 1 {
		return "", fmt.Errorf("unit has %d User= and %d Group= directives", userN, groupN)
	}
	return strings.Join(lines, "\n"), nil
}

// grantGatewayKeys adds the index gateway's signing-key credentials. The
// shipped template does not name them; the index drop-in does, and only that
// one unit may receive them. The rendered isolated unit carries the same
// lines so the gateway user is the one granted gatewaykeys.Dir.
func grantGatewayKeys(unit string) (string, error) {
	block := gatewayKeyLines()
	if strings.Contains(unit, block) {
		return unit, nil
	}
	const marker = "[Install]\n"
	if !strings.Contains(unit, marker) {
		return "", fmt.Errorf("gateway unit has no [Install] section")
	}
	return strings.Replace(unit, marker, block+marker, 1), nil
}

func gatewayKeyLines() string {
	base := filepath.Join(gatewaykeys.Dir, constants.IndexNamespace)
	rsa := filepath.Join(base, constants.GatewayRSAKeyFileName)
	ed := filepath.Join(base, constants.GatewayEdDSAKeyFileName)
	return "LoadCredential=jwt-signing-key:-" + rsa + "\n" +
		"LoadCredential=jwt-eddsa-key:-" + ed + "\n"
}

// isolateIPFSSecrets replaces the ipfs unit's read of the whole secrets
// directory with a bind of swarm.key only, and makes the gateway key directory
// inaccessible. The shipped unit's ReadOnlyPaths line is the whole directory,
// which includes the cluster secret the gateway binds.
func isolateIPFSSecrets(unit string) (string, error) {
	const old = "ReadOnlyPaths=/opt/orama/.orama/secrets"
	if strings.Count(unit, old) != 1 {
		return "", fmt.Errorf("ipfs unit has %d %q lines", strings.Count(unit, old), old)
	}
	repl := "TemporaryFileSystem=/opt/orama/.orama/secrets:ro\n" +
		"BindReadOnlyPaths=/opt/orama/.orama/secrets/swarm.key\n" +
		"InaccessiblePaths=" + gatewaykeys.Dir
	return strings.Replace(unit, old, repl, 1), nil
}

// UnitGrants reports whether unit's sandbox lets its process read path.
// A directory counts as granted when the unit can read it or a file inside it.
// InaccessiblePaths and TemporaryFileSystem hide a path; a more specific bind
// or credential still grants that one file.
func UnitGrants(unit, path string) bool {
	grants, denies := sandboxPaths(unit)
	path = filepath.Clean(path)
	var best string
	for _, g := range grants {
		if covers(g, path) && (best == "" || len(g) > len(best)) {
			best = g
		}
	}
	if best != "" {
		for _, d := range denies {
			if covers(d, path) && len(filepath.Clean(d)) >= len(best) {
				return false
			}
		}
		return true
	}
	for _, g := range grants {
		if path == g || !covers(path, g) {
			continue
		}
		denied := false
		for _, d := range denies {
			if covers(d, g) {
				denied = true
				break
			}
		}
		if !denied {
			return true
		}
	}
	return false
}

func sandboxPaths(unit string) (grants, denies []string) {
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "ReadWritePaths", "ReadOnlyPaths", "BindReadOnlyPaths", "BindPaths":
			grants = append(grants, splitPaths(val)...)
		case "LoadCredential", "LoadCredentialEncrypted":
			if p, ok := credentialPath(val); ok {
				grants = append(grants, p)
			}
		case "InaccessiblePaths", "TemporaryFileSystem":
			denies = append(denies, splitPaths(val)...)
		}
	}
	return grants, denies
}

func splitPaths(val string) []string {
	var out []string
	for _, field := range strings.Fields(val) {
		field = strings.TrimPrefix(field, "-")
		field = strings.TrimSuffix(field, ":ro")
		if field == "" {
			continue
		}
		out = append(out, filepath.Clean(field))
	}
	return out
}

func credentialPath(val string) (string, bool) {
	_, path, ok := strings.Cut(val, ":")
	if !ok {
		return "", false
	}
	path = strings.TrimPrefix(path, "-")
	if path == "" {
		return "", false
	}
	return filepath.Clean(path), true
}

func covers(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
