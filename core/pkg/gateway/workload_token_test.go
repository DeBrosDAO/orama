package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

type fakeDeploymentRegistry struct {
	rqlite.Client
	exists bool
	err    error
}

func (f fakeDeploymentRegistry) Query(_ context.Context, dest any, _ string, _ ...any) error {
	if f.err != nil {
		return f.err
	}
	if f.exists {
		*(dest.(*[]struct {
			ID string `db:"id"`
		})) = []struct {
			ID string `db:"id"`
		}{{ID: "d1"}}
	}
	return nil
}

type fakeIdentities struct{ ensured, minted int }

func (f *fakeIdentities) EnsureWorkloadPrincipal(context.Context, string, string) error {
	f.ensured++
	return nil
}

func (f *fakeIdentities) MintWorkloadToken(context.Context, string, string) (string, time.Time, error) {
	f.minted++
	return "tok", time.Now().Add(time.Hour), nil
}

func TestWorkloadTokenMinter_mintsForADeploymentThatExists(t *testing.T) {
	ids := &fakeIdentities{}
	tok, err := workloadTokenMinter(ids, fakeDeploymentRegistry{exists: true})(context.Background(), "acme", "api")
	if err != nil || tok != "tok" || ids.ensured != 1 || ids.minted != 1 {
		t.Errorf("token %q, err %v, ensured %d, minted %d", tok, err, ids.ensured, ids.minted)
	}
}

// A mint that raced the delete records no principal and issues no token.
func TestWorkloadTokenMinter_refusesADeletedDeployment(t *testing.T) {
	ids := &fakeIdentities{}
	_, err := workloadTokenMinter(ids, fakeDeploymentRegistry{})(context.Background(), "acme", "api")
	if !errors.Is(err, process.ErrDeploymentGone) || ids.ensured != 0 || ids.minted != 0 {
		t.Errorf("err %v, ensured %d, minted %d; want ErrDeploymentGone and nothing recorded", err, ids.ensured, ids.minted)
	}
}

// An unreadable registry is not "gone": a restart may still run on the staged
// token, which only an error that is not ErrDeploymentGone allows.
func TestWorkloadTokenMinter_anUnreadableRegistryIsNotGone(t *testing.T) {
	ids := &fakeIdentities{}
	_, err := workloadTokenMinter(ids, fakeDeploymentRegistry{err: errors.New("no leader")})(context.Background(), "acme", "api")
	if err == nil || errors.Is(err, process.ErrDeploymentGone) || ids.minted != 0 {
		t.Errorf("err %v, minted %d", err, ids.minted)
	}
}

// A refresh mints and writes nothing else: no principal, no row lookup.
func TestWorkloadTokenRefresher_onlyMints(t *testing.T) {
	ids := &fakeIdentities{}
	tok, err := workloadTokenRefresher(ids)(context.Background(), "acme", "api")
	if err != nil || tok != "tok" || ids.ensured != 0 || ids.minted != 1 {
		t.Errorf("token %q, err %v, ensured %d, minted %d", tok, err, ids.ensured, ids.minted)
	}
}
