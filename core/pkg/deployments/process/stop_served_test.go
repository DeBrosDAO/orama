package process

import (
	"context"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

// TestStop_aServedDeploymentHasNothingToStop: a static site has no unit, so
// deleting one must not fail on the stop (every static delete answered 500).
func TestStop_aServedDeploymentHasNothingToStop(t *testing.T) {
	for _, typ := range []deployments.DeploymentType{deployments.DeploymentTypeStatic, deployments.DeploymentTypeNextJSStatic, deployments.DeploymentTypeGoWASM} {
		var calls [][]string
		m := NewManager(zap.NewNop(), Config{Systemctl: func(args ...string) error {
			calls = append(calls, args)
			return nil
		}})
		if err := m.Stop(context.Background(), &deployments.Deployment{Namespace: "acme", Name: "site", Type: typ}); err != nil {
			t.Errorf("%s: Stop = %v, want nil", typ, err)
		}
		if len(calls) != 0 {
			t.Errorf("%s: systemctl was called for a deployment with no unit: %v", typ, calls)
		}
	}
}

func TestStop_aRunDeploymentStillReportsAFailedStop(t *testing.T) {
	m := NewManager(zap.NewNop(), Config{Systemctl: func(args ...string) error {
		return errors.New("refused")
	}})
	err := m.Stop(context.Background(), &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend})
	if err == nil {
		t.Fatal("a refused stop of a Go backend returned nil")
	}
}

func TestRuntimeFor_servedTypesAreErrServedNotRun(t *testing.T) {
	if _, _, err := RuntimeFor(&deployments.Deployment{Type: deployments.DeploymentTypeStatic}); !errors.Is(err, ErrServedNotRun) {
		t.Errorf("static: %v, want ErrServedNotRun", err)
	}
	if _, _, err := RuntimeFor(&deployments.Deployment{Type: deployments.DeploymentTypeGoBackend}); err != nil {
		t.Errorf("go-backend: %v", err)
	}
}
