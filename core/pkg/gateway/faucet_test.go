package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/chainfaucet"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

func TestNewFaucet_noKeyFileMeansNoFaucet(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	svc, err := newFaucet(context.Background(), &Config{}, logger)
	if err != nil || svc != nil {
		t.Fatalf("newFaucet = %v, %v; a gateway with no key file serves no faucet", svc, err)
	}
}

func TestNewFaucet_aKeyFileStartsTheFaucetForItsAccount(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	path := filepath.Join(t.TempDir(), chainfaucet.KeyFileName)
	key, _, err := chainfaucet.CreateKeyFile(path, os.Geteuid(), os.Getegid())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc, err := newFaucet(ctx, &Config{FaucetKeyFile: path}, logger)

	if err != nil || svc == nil || svc.Address() != key.Address() {
		t.Fatalf("newFaucet = %v, %v", svc, err)
	}
}

// A gateway asked to serve a faucet that cannot is an error that names the file, not a gateway
// that quietly serves none.
func TestNewFaucet_aKeyFileThatCannotBeUsedStopsTheGateway(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	loose := filepath.Join(t.TempDir(), chainfaucet.KeyFileName)
	if _, _, err := chainfaucet.CreateKeyFile(loose, os.Geteuid(), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"absent":   filepath.Join(t.TempDir(), "absent.key"),
		"loose":    loose,
		"a folder": t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			svc, err := newFaucet(context.Background(), &Config{FaucetKeyFile: path}, logger)
			if err == nil || svc != nil {
				t.Fatalf("newFaucet = %v, %v", svc, err)
			}
			if !strings.Contains(err.Error(), "faucet_key_file") || !strings.Contains(err.Error(), path) {
				t.Errorf("err = %v, want it to name the setting and the file", err)
			}
		})
	}
}
