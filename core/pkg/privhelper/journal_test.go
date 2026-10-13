package privhelper

import "testing"

func TestValidate_journal(t *testing.T) {
	for _, argv := range [][]string{
		{"journal", "orama-deploy-go@acme-web.service", "20"},
		{"journal", "orama-deploy-build@acme-web.service", "1"},
		{"journal", "orama-deploy-node@acme-web.service", "1000"},
	} {
		if _, err := Validate(argv); err != nil {
			t.Errorf("%q refused: %v", argv, err)
		}
	}
}

func TestValidate_journalRefusesAnythingElse(t *testing.T) {
	for _, argv := range [][]string{
		{"journal"},
		{"journal", "orama-deploy-go@acme-web.service"},
		{"journal", "orama-deploy-go@acme-web.service", "20", "-f"},
		{"journal", "sshd.service", "20"},
		{"journal", "orama-node.service", "20"},
		{"journal", "orama-namespace-gateway@index.service", "20"},
		{"journal", "-u", "orama-deploy-go@acme-web.service", "20"},
		{"journal", "orama-deploy-go@acme-web.service", "0"},
		{"journal", "orama-deploy-go@acme-web.service", "-5"},
		{"journal", "orama-deploy-go@acme-web.service", "1001"},
		{"journal", "orama-deploy-go@acme-web.service", "ten"},
		{"journal", "orama-deploy-go@acme-web.service", ""},
	} {
		if _, err := Validate(argv); err == nil {
			t.Errorf("%q was allowed", argv)
		}
	}
}

func TestAuthorize_journalIsTheClusterGatewaysAndTheNodes(t *testing.T) {
	inv := mustValidate(t, "journal", "orama-deploy-go@acme-web.service", "20")
	for _, unit := range []string{IndexGatewayUnit, NodeUnit} {
		if err := Authorize(Caller{UID: oramaUID, Unit: unit}, inv); err != nil {
			t.Errorf("%s refused: %v", unit, err)
		}
	}
	for _, unit := range []string{"orama-namespace-gateway@alice.service", "orama-deploy-node@alice-web.service", ""} {
		if err := Authorize(Caller{UID: oramaUID, Unit: unit}, inv); err == nil {
			t.Errorf("%q was allowed to read a deployment's journal", unit)
		}
	}
}
