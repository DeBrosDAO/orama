package systemd

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gatewaykeys"
)

func TestServiceUser_isolationSplitsGatewayAndIPFS(t *testing.T) {
	gw, err := ServiceUser(string(ServiceTypeGateway), true)
	if err != nil {
		t.Fatal(err)
	}
	ipfs, err := ServiceUser(string(ServiceTypeIPFS), true)
	if err != nil {
		t.Fatal(err)
	}
	if gw == ipfs {
		t.Fatalf("isolation on: gateway and ipfs both run as %q", gw)
	}
	if gw == sharedUser || ipfs == sharedUser {
		t.Fatalf("isolation on still uses %s (gateway %q, ipfs %q)", sharedUser, gw, ipfs)
	}

	gwOff, err := ServiceUser(string(ServiceTypeGateway), false)
	if err != nil {
		t.Fatal(err)
	}
	ipfsOff, err := ServiceUser(string(ServiceTypeIPFS), false)
	if err != nil {
		t.Fatal(err)
	}
	if gwOff != sharedUser || ipfsOff != sharedUser {
		t.Fatalf("isolation off: gateway %q ipfs %q, want %s", gwOff, ipfsOff, sharedUser)
	}
}

func TestServiceUser_unknownService(t *testing.T) {
	if _, err := ServiceUser("orama-global-evil", true); err == nil {
		t.Fatal("unknown service was given an account")
	}
}

func TestRenderNamespaceUnit_gatewayAndIPFSDoNotShareAUser(t *testing.T) {
	dir := unitDir(t)
	gw, err := RenderNamespaceUnit(dir, string(ServiceTypeGateway), true)
	if err != nil {
		t.Fatal(err)
	}
	ipfs, err := RenderNamespaceUnit(dir, string(ServiceTypeIPFS), true)
	if err != nil {
		t.Fatal(err)
	}
	gwUser, err := directive(gw, "User")
	if err != nil {
		t.Fatal(err)
	}
	ipfsUser, err := directive(ipfs, "User")
	if err != nil {
		t.Fatal(err)
	}
	if gwUser == ipfsUser {
		t.Fatalf("rendered units share User=%s", gwUser)
	}
	wantGW, err := ServiceUser(string(ServiceTypeGateway), true)
	if err != nil {
		t.Fatal(err)
	}
	wantIPFS, err := ServiceUser(string(ServiceTypeIPFS), true)
	if err != nil {
		t.Fatal(err)
	}
	if gwUser != wantGW || ipfsUser != wantIPFS {
		t.Fatalf("users gateway %q ipfs %q, want %q and %q", gwUser, ipfsUser, wantGW, wantIPFS)
	}
}

func TestRenderNamespaceUnit_noTemplateIsGrantedTheGatewayKeys(t *testing.T) {
	dir := unitDir(t)
	gw, err := RenderNamespaceUnit(dir, string(ServiceTypeGateway), true)
	if err != nil {
		t.Fatal(err)
	}
	ipfs, err := RenderNamespaceUnit(dir, string(ServiceTypeIPFS), true)
	if err != nil {
		t.Fatal(err)
	}
	ipfsUser, err := directive(ipfs, "User")
	if err != nil {
		t.Fatal(err)
	}
	// No template unit is granted the gateway key directory: the keys belong to
	// the index instance alone, through its drop-in (pkg/install/gateway_unit.go),
	// and a template-wide grant would hand them to every tenant gateway.
	if UnitGrants(ipfs, gatewaykeys.Dir) {
		t.Fatalf("a process running as %s is granted %s\n%s", ipfsUser, gatewaykeys.Dir, ipfs)
	}
	if UnitGrants(gw, gatewaykeys.Dir) {
		t.Fatalf("the gateway template is granted %s, so every tenant gateway would receive the index signing keys\n%s", gatewaykeys.Dir, gw)
	}
	const clusterSecret = "/opt/orama/.orama/secrets/cluster-secret"
	const swarmKey = "/opt/orama/.orama/secrets/swarm.key"
	if UnitGrants(ipfs, clusterSecret) {
		t.Fatalf("ipfs unit can read %s", clusterSecret)
	}
	if !UnitGrants(ipfs, swarmKey) {
		t.Fatal("ipfs unit can no longer read swarm.key")
	}
	if !UnitGrants(gw, clusterSecret) {
		t.Fatal("gateway unit lost cluster-secret")
	}
}

func TestRenderNamespaceUnit_isolationOffDoesNotRewriteTheShippedUnit(t *testing.T) {
	dir := unitDir(t)
	for _, service := range []ServiceType{ServiceTypeGateway, ServiceTypeIPFS} {
		got, err := RenderNamespaceUnit(dir, string(service), false)
		if err != nil {
			t.Fatal(err)
		}
		shipped := readUnit(t, "orama-namespace-"+string(service)+"@.service")
		if got != shipped {
			t.Fatalf("%s isolation off changed the shipped unit", service)
		}
	}
}

func TestServiceUser_everySharedOramaTemplateHasItsOwnAccount(t *testing.T) {
	seen := map[string]string{}
	for service, id := range serviceIdentities {
		if id.shared != sharedUser {
			continue
		}
		user, err := ServiceUser(service, true)
		if err != nil {
			t.Fatal(err)
		}
		if user == sharedUser {
			t.Errorf("%s isolation still uses %s", service, sharedUser)
		}
		// ipfs-gc operates the ipfs repository, so it keeps that account.
		if service == string(ServiceTypeIPFSGC) {
			continue
		}
		if prev, ok := seen[user]; ok {
			t.Errorf("isolation gives %s to both %s and %s", user, prev, service)
		}
		seen[user] = service
	}
}

func directive(unit, key string) (string, error) {
	var found string
	n := 0
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, key+"=") {
			found = strings.TrimPrefix(line, key+"=")
			n++
		}
	}
	if n != 1 {
		return "", errDirective(key, n)
	}
	return found, nil
}

type directiveError struct {
	key string
	n   int
}

func (e directiveError) Error() string {
	return e.key + " appears a wrong number of times"
}

func errDirective(key string, n int) error {
	return directiveError{key: key, n: n}
}
