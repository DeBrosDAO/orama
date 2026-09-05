package process

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
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
		t.Errorf("%q does not start with %q, so the sudoers grant does not cover it", got, UnitPrefix)
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
