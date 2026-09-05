package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The unit a deployment runs as is a template shipped with the release, not a
// file this package writes: the gateway used to `tee` one into /etc, which only
// worked because it ran as root, and the hardened gateway unit takes that away.
// These read the templates that actually ship.

// deployTemplate returns one of the shipped deployment templates.
func deployTemplate(t *testing.T, runtime Runtime) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "systemd", "orama-deploy-"+string(runtime)+"@.service")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// repoRoot walks up to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

func eachDeployTemplate(t *testing.T, check func(t *testing.T, runtime Runtime, unit string)) {
	t.Helper()
	for _, runtime := range []Runtime{RuntimeNode, RuntimeNPM, RuntimeGo} {
		t.Run(string(runtime), func(t *testing.T) {
			check(t, runtime, deployTemplate(t, runtime))
		})
	}
}

func TestDeployTemplate_doesNotRunTheTenantsCodeAsRoot(t *testing.T) {
	eachDeployTemplate(t, func(t *testing.T, _ Runtime, unit string) {
		if !strings.Contains(unit, "DynamicUser=yes") {
			t.Error("no DynamicUser: the deployment runs as whatever systemd defaults to, which is root")
		}
		if strings.Contains(unit, "User=root") {
			t.Error("the unit names root outright")
		}
	})
}

func TestDeployTemplate_carriesTheWholeHardeningSet(t *testing.T) {
	want := []string{
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
	}
	eachDeployTemplate(t, func(t *testing.T, _ Runtime, unit string) {
		for _, directive := range want {
			if !strings.Contains(unit, directive) {
				t.Errorf("missing %s", directive)
			}
		}
	})
}

// A deployment sits on the same host as the WireGuard overlay that carries
// rqlite, Olric, the node agent and every other namespace's services.
func TestDeployTemplate_keepsTheTenantOffTheOverlay(t *testing.T) {
	eachDeployTemplate(t, func(t *testing.T, _ Runtime, unit string) {
		if !strings.Contains(unit, "IPAddressDeny=") {
			t.Fatal("nothing denies the overlay")
		}
		for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"} {
			if !strings.Contains(unit, cidr) {
				t.Errorf("%s is reachable from tenant code", cidr)
			}
		}
		if !strings.Contains(unit, "IPAddressAllow=localhost") {
			t.Error("loopback is denied, so the node's own reverse proxy cannot reach the app")
		}
	})
}

// The tenant's secrets are in the environment file, which is root-owned and
// read by systemd as PID 1 before it drops to the deployment's own user. A
// value in the unit would be readable by anyone who can run `systemctl cat`.
func TestDeployTemplate_takesItsEnvironmentFromAFileAndNotFromTheUnit(t *testing.T) {
	eachDeployTemplate(t, func(t *testing.T, _ Runtime, unit string) {
		if !strings.Contains(unit, "EnvironmentFile=") {
			t.Error("no EnvironmentFile")
		}
		for _, line := range strings.Split(unit, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "Environment=") {
				t.Errorf("a value is inlined in the unit: %s", line)
			}
		}
	})
}

// Everything that varies per deployment has to come from the instance or the
// environment file, because nothing writes a unit per deployment any more.
func TestDeployTemplate_derivesEveryPathFromTheInstance(t *testing.T) {
	eachDeployTemplate(t, func(t *testing.T, _ Runtime, unit string) {
		for _, directive := range []string{"WorkingDirectory=", "EnvironmentFile=", "StateDirectory=", "CacheDirectory="} {
			line := directiveValue(t, unit, directive)
			if !strings.Contains(line, "%i") {
				t.Errorf("%s%s does not derive from the instance, so one template cannot serve every deployment",
					directive, line)
			}
		}
	})
}

// systemd expands a variable in an argument but never in the executable, which
// is the whole reason there is a template per runtime rather than one template
// the gateway fills in.
func TestDeployTemplate_startsWithALiteralExecutable(t *testing.T) {
	eachDeployTemplate(t, func(t *testing.T, _ Runtime, unit string) {
		exec := directiveValue(t, unit, "ExecStart=")
		binary := strings.Fields(exec)[0]
		if !strings.HasPrefix(binary, "/") {
			t.Errorf("ExecStart begins with %q, which systemd will not resolve", binary)
		}
		if strings.Contains(binary, "$") {
			t.Errorf("ExecStart's executable is a variable (%q); systemd does not expand one there", binary)
		}
	})
}

func TestDeployTemplate_boundsWhatOneDeploymentCanTakeFromTheNode(t *testing.T) {
	eachDeployTemplate(t, func(t *testing.T, _ Runtime, unit string) {
		for _, directive := range []string{"MemoryMax=", "CPUQuota=", "TasksMax=", "MemorySwapMax=0"} {
			if !strings.Contains(unit, directive) {
				t.Errorf("missing %s: one deployment can take the node down", directive)
			}
		}
	})
}

// directiveValue returns the value of the first occurrence of a directive.
func directiveValue(t *testing.T, unit, directive string) string {
	t.Helper()
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, directive) {
			return strings.TrimPrefix(line, directive)
		}
	}
	t.Fatalf("no %s in the unit", directive)
	return ""
}

func TestPlatformEnv_tellsTheDeploymentWhoAndWhereItIs(t *testing.T) {
	env := platformEnv("acme", "orama-deploy-acme-api", "https://ns-acme.dbrs.space", "server.js", 8080)

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
	env := platformEnv("acme", "orama-deploy-acme-api", "", "server.js", 8080)
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
		platformEnv("acme", "orama-deploy-acme-api", "https://ns-acme.dbrs.space", "server.js", 8080),
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
	written := platformEnv("acme", "orama-deploy-acme-api", "https://ns-acme.dbrs.space", "server.js", 8080)
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
