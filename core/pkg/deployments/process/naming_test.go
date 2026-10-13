package process

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// The systemd instance and the deployment's directory have to agree exactly:
// the template derives its WorkingDirectory from %i, so a directory the gateway
// creates under a different name is a unit that starts in the wrong place, or
// does not start at all.
func TestInstanceName_isWhatTheDirectoryIsCalled(t *testing.T) {
	const base = "/opt/orama/.orama/data/deployments"
	dir := DeployDir(base, "acme.test", "my.app")
	instance := InstanceName("acme.test", "my.app")

	if instance != "acme-test-my-app" {
		t.Errorf("instance = %q, want dots replaced: systemd reads a dot as the unit suffix", instance)
	}
	if dir != base+"/"+instance {
		t.Errorf("directory %q is not %q, so WorkingDirectory=%%i points somewhere else", dir, instance)
	}
	if strings.Contains(instance, "/") {
		t.Error("the instance carries a slash, which cannot appear in a unit name")
	}
}

func TestUnitName_namesTheRuntimeAndTheInstance(t *testing.T) {
	got := UnitName(RuntimeNode, "acme", "web")
	if got != "orama-deploy-node@acme-web.service" {
		t.Errorf("UnitName = %q", got)
	}
	if !strings.HasPrefix(got, UnitPrefix) {
		t.Errorf("%q does not start with %q, so the privileged helper refuses it", got, UnitPrefix)
	}
}

func TestRuntimeFor_picksTheTemplateAndTheEntryPoint(t *testing.T) {
	cases := []struct {
		name       string
		deployment *deployments.Deployment
		runtime    Runtime
		entry      string
	}{
		{
			name:       "next.js is served by node from the standalone root",
			deployment: &deployments.Deployment{Type: deployments.DeploymentTypeNextJS},
			runtime:    RuntimeNode,
			entry:      "server.js",
		},
		{
			name:       "node with no entry point defaults to index.js",
			deployment: &deployments.Deployment{Type: deployments.DeploymentTypeNodeJSBackend},
			runtime:    RuntimeNode,
			entry:      "index.js",
		},
		{
			name: "node with an entry point uses it",
			deployment: &deployments.Deployment{
				Type:        deployments.DeploymentTypeNodeJSBackend,
				Environment: map[string]string{"ENTRY_POINT": "dist/main.js"},
			},
			runtime: RuntimeNode,
			entry:   "dist/main.js",
		},
		{
			name: "npm:start is its own template, because the interpreter differs",
			deployment: &deployments.Deployment{
				Type:        deployments.DeploymentTypeNodeJSBackend,
				Environment: map[string]string{"ENTRY_POINT": "npm:start"},
			},
			runtime: RuntimeNPM,
			entry:   "",
		},
		{
			name:       "go runs the binary the builder produced",
			deployment: &deployments.Deployment{Type: deployments.DeploymentTypeGoBackend},
			runtime:    RuntimeGo,
			entry:      "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime, entry, err := RuntimeFor(tc.deployment)
			if err != nil {
				t.Fatalf("RuntimeFor: %v", err)
			}
			if runtime != tc.runtime || entry != tc.entry {
				t.Errorf("got (%q, %q), want (%q, %q)", runtime, entry, tc.runtime, tc.entry)
			}
		})
	}
}

// A static site has no process — the gateway serves it from IPFS — so asking
// which unit runs it is a mistake, not a case with a sensible default. Falling
// back to one would start a unit that runs `echo` and reports healthy.
func TestRuntimeFor_refusesADeploymentThatIsServedRatherThanRun(t *testing.T) {
	_, _, err := RuntimeFor(&deployments.Deployment{Type: deployments.DeploymentTypeStatic})
	if err == nil {
		t.Fatal("a static deployment was given a runtime")
	}
	if !strings.Contains(err.Error(), "served rather than run") {
		t.Errorf("error does not say why: %v", err)
	}
}

func TestValidateName_acceptsWhatTheHelperAccepts(t *testing.T) {
	for _, name := range []string{"web", "my-app", "api_v2", "A1", strings.Repeat("a", MaxNameLength)} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateName_refusesWhatTheHelperWouldRefuse(t *testing.T) {
	for name, why := range map[string]string{
		"":                                   "empty",
		"-web":                               "leading dash",
		"_web":                               "leading underscore",
		"my.app":                             "a dot is the unit suffix",
		"my app":                             "space",
		"web/../../etc":                      "path separator",
		"web\n":                              "newline",
		"wéb":                                "non-ASCII",
		strings.Repeat("a", MaxNameLength+1): "one over the limit",
	} {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) accepted a name with a %s", name, why)
		}
	}
}

// The longest namespace plus the longest name must still be an instance
// orama-privhelper will start; otherwise a valid name fails at start, which is
// what ValidateName exists to prevent.
func TestValidateName_longestNameFitsTheHelpersInstance(t *testing.T) {
	const maxNamespaceLength = 64
	longestNamespace := strings.Repeat("n", maxNamespaceLength)
	if !httputil.ValidateNamespace(longestNamespace) || httputil.ValidateNamespace(longestNamespace+"n") {
		t.Fatalf("namespaces are no longer capped at %d characters; MaxNameLength was derived from it", maxNamespaceLength)
	}
	unit := UnitName(RuntimeNode, longestNamespace, strings.Repeat("a", MaxNameLength))
	if _, err := privhelper.Validate([]string{privhelper.ToolSystemctl, "start", unit}); err != nil {
		t.Fatalf("the longest valid deployment is refused by orama-privhelper: %v", err)
	}
	if _, err := privhelper.Validate([]string{privhelper.ToolDeploy, "set-env", InstanceName(longestNamespace, strings.Repeat("a", MaxNameLength))}); err != nil {
		t.Fatalf("the longest valid deployment's environment is refused by orama-privhelper: %v", err)
	}
}

func TestValidateInstance_legacyNamesStayValid(t *testing.T) {
	for _, c := range [][2]string{{"acme", "my.app"}, {"acme", "web"}, {"acme", strings.Repeat("a", MaxNameLength+10)}} {
		if err := ValidateInstance(c[0], c[1]); err != nil {
			t.Errorf("%s/%s: %v", c[0], c[1], err)
		}
	}
}

func TestValidateInstance_refusesWhatCouldNeverStart(t *testing.T) {
	for _, c := range [][2]string{{"acme", ""}, {"acme", "web/../x"}, {"acme", "has space"}, {"acme", strings.Repeat("a", 200)}} {
		if err := ValidateInstance(c[0], c[1]); err == nil {
			t.Errorf("%s/%q was accepted", c[0], c[1])
		}
	}
}

// The cap is on the tenant's variables where they are set; the helper's is on
// the file it stages, which adds the platform's. A tenant environment at the
// cap, merged with the largest platform environment, must still render and be
// staged — otherwise it is accepted and then fails at every start.
func TestMaxEnvFileBytes_fullTenantEnvironmentStillStarts(t *testing.T) {
	longNS := strings.Repeat("n", 64)
	longName := strings.Repeat("a", MaxNameLength)
	tenant := map[string]string{}
	for i := 0; ; i++ {
		key := fmt.Sprintf("K%04d", i)
		tenant[key] = strings.Repeat("v", 1000)
		if deployments.ValidateEnvSize(tenant) != nil {
			delete(tenant, key)
			break
		}
	}
	merged := mergeEnv(tenant, platformEnv(longNS, UnitPrefix+InstanceName(longNS, longName),
		"https://ns-"+longNS+".example.com", strings.Repeat("e", 255), 65535))
	rendered, err := deployments.RenderEnvFile(merged)
	if err != nil {
		t.Fatalf("a tenant environment at the cap does not render with the platform's: %v", err)
	}
	if len(rendered) > privhelper.MaxDeploySecretBytes {
		t.Fatalf("rendered %d bytes; orama-privhelper stages at most %d", len(rendered), privhelper.MaxDeploySecretBytes)
	}
	if err := privhelper.CheckDeployInput("set-env", []byte(rendered)); err != nil {
		t.Fatalf("orama-privhelper would refuse it: %v", err)
	}
}
