package privhelper

import (
	"strings"
	"testing"
)

func TestDeploymentDirForInstance(t *testing.T) {
	got, err := DeploymentDirForInstance("alice-web")
	if err != nil || got != "/opt/orama/.orama/data/deployments/alice-web" {
		t.Fatalf("%q, %v", got, err)
	}
	for _, bad := range []string{"", "../etc", "alice/web", "alice web", "-alice"} {
		if _, err := DeploymentDirForInstance(bad); err == nil {
			t.Errorf("instance %q was accepted", bad)
		}
	}
}

func TestDeploymentDirToVerify(t *testing.T) {
	for argv, want := range map[string]string{
		"systemctl start orama-deploy-node@alice-web.service":   "/opt/orama/.orama/data/deployments/alice-web",
		"systemctl restart orama-deploy-go@alice-api.service":   "/opt/orama/.orama/data/deployments/alice-api",
		"systemctl start orama-deploy-npm@a-b-c.service":        "/opt/orama/.orama/data/deployments/a-b-c",
		"systemctl start orama-deploy-build@alice-web.service":  "/opt/orama/.orama/data/deployments/alice-web",
		"systemctl start orama-deploy-clean@alice-web.service":  "",
		"systemctl stop orama-deploy-node@alice-web.service":    "",
		"systemctl enable orama-deploy-node@alice-web.service":  "",
		"systemctl start orama-namespace-gateway@alice.service": "",
		"systemctl daemon-reload":                               "",
	} {
		inv := mustValidate(t, strings.Fields(argv)...)
		if got := DeploymentDirToVerify(inv); got != want {
			t.Errorf("%s: %q, want %q", argv, got, want)
		}
	}
}
