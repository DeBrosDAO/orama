package systemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renderInstalled is what InstallTemplateUnits writes for template.
func renderInstalled(t *testing.T, template string) string {
	t.Helper()
	data, err := renderTemplateUnit(unitDir(t), template)
	if err != nil {
		t.Fatalf("render %s: %v", template, err)
	}
	return string(data)
}

func TestRenderTemplateUnit_isolatedServicesRunAsTheirOwnAccount(t *testing.T) {
	for _, s := range IsolatedServices() {
		unit := renderInstalled(t, "orama-namespace-"+s.Service+"@.service")
		want, err := ServiceUser(s.Service, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"User", "Group"} {
			got, err := directive(unit, key)
			if err != nil {
				t.Fatalf("%s: %v", s.Service, err)
			}
			if got != want {
				t.Errorf("%s: installed %s=%s, want %s", s.Service, key, got, want)
			}
		}
		if got, err := directive(unit, "ProtectProc"); err != nil || got != "invisible" {
			t.Errorf("%s: ProtectProc=%q (%v), want invisible", s.Service, got, err)
		}
	}
}

// Every template that is not isolated is installed exactly as shipped, so a
// service whose files are still shared with orama keeps running as orama.
func TestRenderTemplateUnit_sharedServicesAreCopiedUnchanged(t *testing.T) {
	for _, template := range UnitFilesToInstall() {
		if service, ok := namespaceTemplateService(template); ok && Isolated(service) {
			continue
		}
		if got := renderInstalled(t, template); got != readUnit(t, template) {
			t.Errorf("%s: installed content differs from the shipped file", template)
		}
	}
	for _, service := range []ServiceType{ServiceTypeGateway, ServiceTypeRQLite, ServiceTypeOlric, ServiceTypePubsub, ServiceTypeIPFS, ServiceTypeCaddy} {
		unit := renderInstalled(t, "orama-namespace-"+string(service)+"@.service")
		if got, err := directive(unit, "User"); err != nil || got != sharedUser {
			t.Errorf("%s: User=%q (%v), want %s until its files stop being shared", service, got, err, sharedUser)
		}
	}
}

// CoreDNS reads nothing under /opt/orama: none of it is granted, the cluster
// secret and every namespace's configs included.
func TestRenderTemplateUnit_coreDNSSeesNothingOfTheOramaTree(t *testing.T) {
	unit := renderInstalled(t, "orama-namespace-coredns@.service")
	for _, path := range []string{
		"/opt/orama/.orama/secrets/cluster-secret",
		"/opt/orama/.orama/configs/node.yaml",
		"/opt/orama/.orama/data/namespaces/index/configs",
		"/opt/orama/.orama/logs",
	} {
		if UnitGrants(unit, path) {
			t.Errorf("coredns unit is granted %s\n%s", path, unit)
		}
	}
	if !strings.Contains(unit, "TemporaryFileSystem=/opt/orama:ro\n") {
		t.Error("coredns unit does not replace /opt/orama with an empty read-only tmpfs")
	}
}

// The SFU sees its own namespace's configs and nothing else of the tree, and
// runs a binary its account can execute.
func TestRenderTemplateUnit_sfuSeesOnlyItsOwnNamespaceConfigs(t *testing.T) {
	unit := renderInstalled(t, "orama-namespace-sfu@.service")
	if !UnitGrants(unit, "/opt/orama/.orama/data/namespaces/%i/configs/sfu-node-1.yaml") {
		t.Errorf("sfu unit cannot read its own config\n%s", unit)
	}
	for _, path := range []string{
		"/opt/orama/.orama/secrets/cluster-secret",
		"/opt/orama/.orama/data/namespaces/other/configs/sfu-node-1.yaml",
		"/opt/orama/.orama/data/namespaces/%i/gateway",
		"/opt/orama/bin/sfu",
	} {
		if UnitGrants(unit, path) {
			t.Errorf("sfu unit is granted %s\n%s", path, unit)
		}
	}
	exec, err := directive(unit, "ExecStart")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(exec, "/usr/local/bin/sfu ") {
		t.Errorf("sfu runs %q; /opt/orama/bin is root:orama 0750 and orama-sfu cannot execute from it", exec)
	}
}

// These units already had their own sandbox but not ProtectProc=invisible, so
// their process could list every other process on the host.
func TestTemplateUnits_hideOtherUsersProcesses(t *testing.T) {
	for _, name := range []string{
		"orama-namespace-sni-router@.service",
		"orama-namespace-vault@.service",
		"orama-namespace-ntfy@.service",
		HostTURNServiceName,
	} {
		if got, err := directive(readUnit(t, name), "ProtectProc"); err != nil || got != "invisible" {
			t.Errorf("%s: ProtectProc=%q (%v), want invisible", name, got, err)
		}
	}
}

// A shipped isolated template that has lost its User= line cannot be given
// an account; install fails naming it instead of installing a unit that runs
// as root.
func TestRenderTemplateUnit_isolatedTemplateWithoutUserFails(t *testing.T) {
	dir := t.TempDir()
	const template = "orama-namespace-coredns@.service"
	if err := os.WriteFile(filepath.Join(dir, template), []byte("[Service]\nExecStart=/usr/local/bin/coredns\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := renderTemplateUnit(dir, template)
	if err == nil || !strings.Contains(err.Error(), template) {
		t.Fatalf("render = %v, want an error naming %s", err, template)
	}
}

func TestRenderTemplateUnit_missingTemplateFails(t *testing.T) {
	for _, template := range []string{"orama-namespace-sfu@.service", "orama-namespace-rqlite@.service"} {
		if _, err := renderTemplateUnit(t.TempDir(), template); err == nil || !strings.Contains(err.Error(), template) {
			t.Errorf("render of a missing %s = %v, want an error naming it", template, err)
		}
	}
}

func TestNamespaceTemplateService_onlyServiceTemplates(t *testing.T) {
	cases := map[string]string{
		"orama-namespace-sfu@.service":          "sfu",
		"orama-namespace-ipfs-cluster@.service": "ipfs-cluster",
	}
	for template, want := range cases {
		if got, ok := namespaceTemplateService(template); !ok || got != want {
			t.Errorf("namespaceTemplateService(%q) = %q, %v; want %q", template, got, ok, want)
		}
	}
	for _, template := range []string{"orama-namespace-ipfs-gc@.timer", HostTURNServiceName, "orama-deploy-go@.service", ""} {
		if got, ok := namespaceTemplateService(template); ok {
			t.Errorf("namespaceTemplateService(%q) = %q; it is not a namespace service template", template, got)
		}
	}
}

func TestServiceGroupID_refusesASharedService(t *testing.T) {
	if _, err := ServiceGroupID(string(ServiceTypeGateway)); err == nil {
		t.Fatal("the gateway, which runs as orama, was given an isolated group")
	}
}

func TestIsolatedServices_isACopy(t *testing.T) {
	got := IsolatedServices()
	if len(got) == 0 {
		t.Fatal("no isolated services")
	}
	got[0].Service = "gateway"
	if Isolated("gateway") {
		t.Fatal("changing the returned slice isolated the gateway")
	}
}
