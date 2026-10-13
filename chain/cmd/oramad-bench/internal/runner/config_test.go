package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/broadcast"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/gen"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/profile"
)

func validConfig() Config {
	return Config{
		Nodes: []string{"tcp://a:1", "tcp://b:2"}, FunderKeyFile: "key", Profile: profile.Smoke(),
	}.WithDefaults()
}

func TestConfigWithDefaults(t *testing.T) {
	c := Config{}.WithDefaults()
	if c.Seed != DefaultSeed || c.Broadcast != broadcast.ModeSync || c.Workers != DefaultWorkers || c.FeeMultiple != ops.DefaultFeeMultiple ||
		c.GasAdjust != ops.DefaultGasAdjustPercent || c.Drain != DefaultDrain || c.Settle != DefaultSettle || c.Parallel != DefaultParallel {
		t.Fatalf("defaults %+v", c)
	}
	set := Config{Seed: "s", Workers: 3, Drain: time.Second}.WithDefaults()
	if set.Seed != "s" || set.Workers != 3 || set.Drain != time.Second {
		t.Fatalf("WithDefaults overwrote what was set: %+v", set)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Config){
		"one node":                      func(c *Config) { c.Nodes = c.Nodes[:1] },
		"no funder key":                 func(c *Config) { c.FunderKeyFile = "" },
		"bad mode":                      func(c *Config) { c.Broadcast = "fire" },
		"no workers":                    func(c *Config) { c.Workers = 0 },
		"empty profile":                 func(c *Config) { c.Profile = profile.Profile{Name: "x", Accounts: 4} },
		"too few account":               func(c *Config) { c.Profile.Accounts = 1 },
		"async with a closed-loop step": func(c *Config) { c.Broadcast = broadcast.ModeAsync },
	}
	for name, mutate := range cases {
		c := validConfig()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadFunder(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.key")
	if err := os.WriteFile(good, []byte(strings.Repeat("ab", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	acct, err := LoadFunder(good)
	if err != nil || !strings.HasPrefix(acct.Address, "orama1") {
		t.Fatalf("got %+v, %v", acct, err)
	}
	for name, content := range map[string]string{"not hex": "zz", "short": "abcd", "zero key": strings.Repeat("00", 32)} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFunder(path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := LoadFunder(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file was accepted")
	}
}

func TestRequireLocalnet(t *testing.T) {
	if err := requireLocalnet("orama-localnet-1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"orama-devnet-1", "orama-stagenet-1", "orama-testnet-1", "orama-1", "orama-1-localnet-x", "mainnet-localnet-1", ""} {
		if err := requireLocalnet(id); !errors.Is(err, ErrNotLocalnet) {
			t.Errorf("chain id %q: %v", id, err)
		}
	}
}

func TestIsShielded(t *testing.T) {
	for _, name := range []string{gen.Shield, gen.Unshield, "shielded-transfer-8"} {
		if !isShielded(name) {
			t.Errorf("%s is a shielded type", name)
		}
	}
	for _, name := range []string{gen.BankSend, gen.CnftMint, "shielded-transfer-3"} {
		if isShielded(name) {
			t.Errorf("%s is not a shielded type", name)
		}
	}
}

func TestDescribeStep(t *testing.T) {
	open := profile.Step{Mode: "open-loop", Rate: 2.5, Duration: 10 * time.Second}
	closed := profile.Step{Mode: "closed-loop", Window: 32, Duration: 20 * time.Second}
	if got := describeStep(open); got != "open-loop 2.5 tx/s for 10s" {
		t.Errorf("open: %q", got)
	}
	if got := describeStep(closed); got != "closed-loop window 32 for 20s" {
		t.Errorf("closed: %q", got)
	}
}
