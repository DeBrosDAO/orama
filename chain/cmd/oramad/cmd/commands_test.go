package cmd

import (
	"bytes"
	"strings"
	"testing"
	"text/template"

	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
)

// A node that serves the public chain query route must not run a query for unbounded gas: the
// app.toml oramad init writes carries a query-gas-limit.
func TestInitAppConfig_setsAQueryGasLimit(t *testing.T) {
	tmpl, cfg := initAppConfig()
	srvCfg, ok := cfg.(*serverconfig.Config)
	if !ok {
		t.Fatalf("app config is %T, want server config", cfg)
	}
	if srvCfg.QueryGasLimit == 0 || srvCfg.QueryGasLimit != defaultQueryGasLimit {
		t.Fatalf("QueryGasLimit = %d, want %d", srvCfg.QueryGasLimit, defaultQueryGasLimit)
	}
	tpl, err := template.New("app.toml").Parse(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, srvCfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "query-gas-limit = \"2000000\"") && !strings.Contains(out.String(), "query-gas-limit = 2000000") {
		t.Fatalf("the rendered app.toml carries no query-gas-limit of %d:\n%s", defaultQueryGasLimit, grep(out.String(), "query-gas-limit"))
	}
}

func grep(s, needle string) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, needle) {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}
