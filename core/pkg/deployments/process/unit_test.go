package process

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
)

func validSpec() UnitSpec {
	return UnitSpec{
		ServiceName:     "orama-deploy-acme-api",
		Namespace:       "acme",
		Name:            "api",
		WorkDir:         "/opt/orama/.orama/deployments/acme/api",
		StartCmd:        "/usr/bin/node server.js",
		EnvFilePath:     "/opt/orama/.orama/deployment-env/orama-deploy-acme-api.env",
		RestartPolicy:   "on-failure",
		MemoryLimitMB:   512,
		CPULimitPercent: 50,
	}
}

func renderOrFail(t *testing.T, spec UnitSpec) string {
	t.Helper()
	unit, err := RenderUnit(spec)
	if err != nil {
		t.Fatalf("RenderUnit: %v", err)
	}
	return unit
}

// A deployment is a tenant's own code running on a node that also runs the
// cluster's control plane. Its unit had no User= at all, so it ran as root.
func TestRenderUnit_doesNotRunTheTenantsCodeAsRoot(t *testing.T) {
	unit := renderOrFail(t, validSpec())

	if strings.Contains(unit, "User=root") {
		t.Fatal("the unit names root as its user")
	}
	if !strings.Contains(unit, "DynamicUser=yes") {
		t.Fatal("the unit has no user of its own, so it runs as whatever systemd defaults to, which is root")
	}
}

// Every directive here was absent, and each one is load-bearing. A test that
// only checked a couple of them would let the rest be dropped by a later edit
// without anything failing.
func TestRenderUnit_carriesTheWholeHardeningSet(t *testing.T) {
	unit := renderOrFail(t, validSpec())

	for _, directive := range []string{
		"DynamicUser=yes",
		"ProtectSystem=strict",
		"ProtectHome=yes",
		"NoNewPrivileges=yes",
		"PrivateDevices=yes",
		"PrivateTmp=yes",
		"ProtectKernelTunables=yes",
		"ProtectKernelModules=yes",
		"ProtectControlGroups=yes",
		"RestrictNamespaces=yes",
		"RestrictSUIDSGID=yes",
		"RestrictRealtime=yes",
		"LockPersonality=yes",
		"RemoveIPC=yes",
		"ProtectProc=invisible",
	} {
		if !strings.Contains(unit, directive) {
			t.Errorf("the unit is missing %s:\n%s", directive, unit)
		}
	}
}

// The WireGuard overlay is the cluster's control plane: rqlite, Olric, and
// every other namespace's services are on it, and a deployment sits on the same
// host. Tenant code has no business there.
func TestRenderUnit_keepsTheTenantOffTheOverlay(t *testing.T) {
	unit := renderOrFail(t, validSpec())

	if !strings.Contains(unit, "IPAddressDeny=") {
		t.Fatal("the unit does not restrict where the deployment may connect")
	}
	deny := ""
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, "IPAddressDeny=") {
			deny = line
		}
	}
	if !strings.Contains(deny, "10.0.0.0/8") {
		t.Errorf("the overlay range is reachable from tenant code: %s", deny)
	}
	if !strings.Contains(deny, "169.254.0.0/16") {
		t.Errorf("the link-local metadata range is reachable from tenant code: %s", deny)
	}
	if !strings.Contains(unit, "IPAddressAllow=localhost") {
		t.Error("loopback is denied, so the node's own reverse proxy cannot reach the app")
	}
}

// The values used to be interpolated into the unit itself. They are in a file
// systemd reads as root, which the deployment's own user never sees.
func TestRenderUnit_takesItsEnvironmentFromAFileAndNotFromTheUnit(t *testing.T) {
	unit := renderOrFail(t, validSpec())

	if !strings.Contains(unit, "EnvironmentFile=/opt/orama/.orama/deployment-env/orama-deploy-acme-api.env") {
		t.Fatalf("the unit does not point at the environment file:\n%s", unit)
	}
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, "Environment=") {
			t.Errorf("a value is written into the unit itself: %s", line)
		}
	}
}

func TestRenderUnit_writesTheDeploymentsOwnLimits(t *testing.T) {
	unit := renderOrFail(t, validSpec())

	for _, want := range []string{"MemoryMax=512M", "MemorySwapMax=0", "CPUQuota=50%", "TasksMax=512"} {
		if !strings.Contains(unit, want) {
			t.Errorf("the unit is missing %s:\n%s", want, unit)
		}
	}
	// MemoryLimit= is the obsolete spelling; systemd maps it but every other
	// unit on the node uses MemoryMax.
	if strings.Contains(unit, "MemoryLimit=") {
		t.Error("the unit uses the obsolete MemoryLimit= spelling")
	}
}

// A deployment with no recorded limit is a deployment with the default limit,
// not one with a limit of zero — MemoryMax=0M would let it allocate nothing.
func TestRenderUnit_unsetLimitsFallToTheDefaultsAndNotToZero(t *testing.T) {
	spec := validSpec()
	spec.MemoryLimitMB = 0
	spec.CPULimitPercent = 0
	unit := renderOrFail(t, spec)

	if strings.Contains(unit, "MemoryMax=0M") || strings.Contains(unit, "CPUQuota=0%") {
		t.Fatalf("an unset limit became a limit of zero:\n%s", unit)
	}
	if !strings.Contains(unit, "MemoryMax=256M") {
		t.Errorf("expected the default memory limit of %dMB:\n%s", deployments.DefaultMemoryLimitMB, unit)
	}
	if !strings.Contains(unit, "CPUQuota=50%") {
		t.Errorf("expected the default CPU limit of %d%%:\n%s", deployments.DefaultCPULimitPercent, unit)
	}
}

func TestRenderUnit_givesTheDeploymentSomewhereToWrite(t *testing.T) {
	unit := renderOrFail(t, validSpec())

	if !strings.Contains(unit, "StateDirectory=orama-deploy-acme-api") {
		t.Errorf("the deployment has no writable directory:\n%s", unit)
	}
	if !strings.Contains(unit, "CacheDirectory=orama-deploy-acme-api") {
		t.Errorf("the deployment has no cache directory:\n%s", unit)
	}
	if got := StateDirectoryPath("orama-deploy-acme-api"); got != "/var/lib/orama-deploy-acme-api" {
		t.Errorf("StateDirectoryPath = %q", got)
	}
	if got := CacheDirectoryPath("orama-deploy-acme-api"); got != "/var/cache/orama-deploy-acme-api" {
		t.Errorf("CacheDirectoryPath = %q", got)
	}
}

func TestRenderUnit_writesTheDescriptionAndIdentity(t *testing.T) {
	unit := renderOrFail(t, validSpec())

	if !strings.Contains(unit, "Description=Orama Deployment - acme/api") {
		t.Error("the unit does not say which deployment it is")
	}
	if !strings.Contains(unit, "SyslogIdentifier=orama-deploy-acme-api") {
		t.Error("the unit's logs are not attributable to the deployment")
	}
	if !strings.Contains(unit, "ExecStart=/usr/bin/node server.js") {
		t.Error("the unit does not start the deployment")
	}
	if !strings.Contains(unit, "Restart=on-failure") {
		t.Error("the unit does not carry the deployment's restart policy")
	}
	if !strings.Contains(unit, "WorkingDirectory=/opt/orama/.orama/deployments/acme/api") {
		t.Error("the unit does not run in the deployment's directory")
	}
}

// Every field is interpolated into a line of a unit file, where a newline
// starts a new directive. "It is constrained upstream" is exactly the
// assumption that put unescaped tenant input into this file to begin with.
func TestRenderUnit_refusesAFieldThatWouldWriteItsOwnDirective(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(*UnitSpec)
	}{
		{"service name", func(s *UnitSpec) { s.ServiceName = "a\nExecStartPre=/bin/sh -c id" }},
		{"namespace", func(s *UnitSpec) { s.Namespace = "a\nUser=root" }},
		{"deployment name", func(s *UnitSpec) { s.Name = "a\nUser=root" }},
		{"working directory", func(s *UnitSpec) { s.WorkDir = "/tmp\nUser=root" }},
		{"start command", func(s *UnitSpec) { s.StartCmd = "/bin/true\nUser=root" }},
		{"environment file", func(s *UnitSpec) { s.EnvFilePath = "/tmp/x\nUser=root" }},
		{"restart policy", func(s *UnitSpec) { s.RestartPolicy = "always\nUser=root" }},
		{"carriage return", func(s *UnitSpec) { s.Name = "a\rUser=root" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := validSpec()
			tc.spoil(&spec)
			if _, err := RenderUnit(spec); err == nil {
				t.Fatalf("RenderUnit accepted a %s carrying a newline", tc.name)
			}
		})
	}
}

func TestRenderUnit_refusesAnIncompleteSpec(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(*UnitSpec)
	}{
		{"no service name", func(s *UnitSpec) { s.ServiceName = "" }},
		{"no namespace", func(s *UnitSpec) { s.Namespace = "" }},
		{"no name", func(s *UnitSpec) { s.Name = "" }},
		{"no work dir", func(s *UnitSpec) { s.WorkDir = "" }},
		{"no start command", func(s *UnitSpec) { s.StartCmd = "" }},
		{"no environment file", func(s *UnitSpec) { s.EnvFilePath = "  " }},
		{"no restart policy", func(s *UnitSpec) { s.RestartPolicy = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := validSpec()
			tc.spoil(&spec)
			if _, err := RenderUnit(spec); err == nil {
				t.Fatalf("RenderUnit wrote a unit with %s", tc.name)
			}
		})
	}
}

func TestPlatformEnv_tellsTheDeploymentWhoAndWhereItIs(t *testing.T) {
	env := platformEnv("acme", "orama-deploy-acme-api", "https://ns-acme.dbrs.space", 8080)

	for key, want := range map[string]string{
		"PORT":              "8080",
		"ORAMA_NAMESPACE":   "acme",
		"ORAMA_GATEWAY_URL": "https://ns-acme.dbrs.space",
		"ORAMA_STATE_DIR":   "/var/lib/orama-deploy-acme-api",
		"ORAMA_CACHE_DIR":   "/var/cache/orama-deploy-acme-api",
	} {
		if env[key] != want {
			t.Errorf("%s = %q, want %q", key, env[key], want)
		}
	}
}

func TestPlatformEnv_omitsTheGatewayURLWhenThereIsNoDomain(t *testing.T) {
	env := platformEnv("acme", "orama-deploy-acme-api", "", 8080)
	if _, present := env["ORAMA_GATEWAY_URL"]; present {
		t.Error("a gateway URL was invented from an empty base domain")
	}
}

// A tenant that could set ORAMA_GATEWAY_URL could point its own app at another
// namespace's gateway, and one that could set PORT would make itself
// unreachable in a way that looks like its own code failing.
func TestMergeEnv_thePlatformsVariablesWinOverTheTenants(t *testing.T) {
	merged := mergeEnv(
		map[string]string{
			"PORT":              "1",
			"ORAMA_NAMESPACE":   "victim",
			"ORAMA_GATEWAY_URL": "https://attacker.example",
			"MY_OWN":            "kept",
		},
		platformEnv("acme", "orama-deploy-acme-api", "https://ns-acme.dbrs.space", 8080),
	)

	if merged["PORT"] != "8080" {
		t.Errorf("PORT = %q, want the allocated port", merged["PORT"])
	}
	if merged["ORAMA_NAMESPACE"] != "acme" {
		t.Errorf("ORAMA_NAMESPACE = %q, want the deployment's own namespace", merged["ORAMA_NAMESPACE"])
	}
	if merged["ORAMA_GATEWAY_URL"] != "https://ns-acme.dbrs.space" {
		t.Errorf("ORAMA_GATEWAY_URL = %q, want the namespace's own gateway", merged["ORAMA_GATEWAY_URL"])
	}
	if merged["MY_OWN"] != "kept" {
		t.Error("the tenant's own variable was dropped")
	}
}

// The reserved-name list in the API and the set the platform actually writes
// have to be the same set, or a tenant can set a name that is then silently
// overwritten.
func TestPlatformEnvKeys_matchesWhatThePlatformActuallyWrites(t *testing.T) {
	written := platformEnv("acme", "orama-deploy-acme-api", "https://ns-acme.dbrs.space", 8080)
	if len(written) != len(PlatformEnvKeys) {
		t.Fatalf("the platform writes %d variables but declares %d: %v vs %v",
			len(written), len(PlatformEnvKeys), written, PlatformEnvKeys)
	}
	for _, key := range PlatformEnvKeys {
		if _, ok := written[key]; !ok {
			t.Errorf("%s is declared as a platform variable but is never written", key)
		}
	}
}

func TestSortedEnv_isStableAndComplete(t *testing.T) {
	got := sortedEnv(map[string]string{"B": "2", "A": "1", "C": "3"})
	want := []string{"A=1", "B=2", "C=3"}
	if len(got) != len(want) {
		t.Fatalf("sortedEnv = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedEnv = %v, want %v", got, want)
		}
	}
}
