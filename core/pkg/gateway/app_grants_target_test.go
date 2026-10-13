package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubDeploymentQuerier answers the existence query with the deployments it
// holds, keyed namespace/name, and counts the questions it was asked.
type stubDeploymentQuerier struct {
	have  map[string]bool
	err   error
	calls int
}

func (s *stubDeploymentQuerier) Query(_ context.Context, dest any, _ string, args ...any) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	rows := dest.(*[]struct {
		ID string `db:"id"`
	})
	if s.have[args[0].(string)+"/"+args[1].(string)] {
		*rows = append(*rows, struct {
			ID string `db:"id"`
		}{ID: "d1"})
	}
	return nil
}

func appGrantPost(body string) *http.Request {
	return withNamespace(httptest.NewRequest(http.MethodPost, "/v1/deployments/grants",
		strings.NewReader(body)), "acme")
}

func TestCheckGrantTarget_acceptsADeploymentOfTheNamespace(t *testing.T) {
	g := &Gateway{deploymentQuerier: &stubDeploymentQuerier{have: map[string]bool{"acme/web": true}}}
	if err := g.checkGrantTarget(context.Background(), "acme", "web"); err != nil {
		t.Fatalf("a deployment of the namespace was refused: %v", err)
	}
}

func TestCheckGrantTarget_refusesANameNoDeploymentCanHave(t *testing.T) {
	q := &stubDeploymentQuerier{have: map[string]bool{"acme/web": true}}
	g := &Gateway{deploymentQuerier: q}
	for name, deployment := range map[string]string{
		"slash":          "web/../../other/api",
		"colon":          "web:admin",
		"another ns":     "other/web",
		"space":          "my app",
		"empty":          "",
		"trailing slash": "web/",
	} {
		t.Run(name, func(t *testing.T) {
			err := g.checkGrantTarget(context.Background(), "acme", deployment)
			if !errors.Is(err, errInvalidGrantName) {
				t.Fatalf("err = %v, want errInvalidGrantName", err)
			}
		})
	}
	if q.calls != 0 {
		t.Errorf("the registry was asked %d times about names that cannot exist", q.calls)
	}
}

// A grant for a name that is no deployment would sit unused until a deployment
// of that name appeared, and then apply to it.
func TestCheckGrantTarget_refusesADeploymentThatDoesNotExist(t *testing.T) {
	g := &Gateway{deploymentQuerier: &stubDeploymentQuerier{have: map[string]bool{"other/web": true}}}
	err := g.checkGrantTarget(context.Background(), "acme", "web")
	if !errors.Is(err, errNoSuchDeployment) {
		t.Fatalf("err = %v, want errNoSuchDeployment: another namespace's deployment counted", err)
	}
}

func TestCheckGrantTarget_anUnreadableRegistryIsNotAnAbsence(t *testing.T) {
	g := &Gateway{deploymentQuerier: &stubDeploymentQuerier{err: errors.New("registry down")}}
	err := g.checkGrantTarget(context.Background(), "acme", "web")
	if err == nil || errors.Is(err, errNoSuchDeployment) || errors.Is(err, errInvalidGrantName) {
		t.Fatalf("err = %v, want a read failure", err)
	}
}

func TestCheckGrantTarget_noRegistryIsAnError(t *testing.T) {
	g := &Gateway{}
	if err := g.checkGrantTarget(context.Background(), "acme", "web"); err == nil {
		t.Fatal("a gateway with no deployment registry vouched for a deployment")
	}
}

func TestSetAppGrant_answersForABadTarget(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		q    *stubDeploymentQuerier
		want int
	}{
		"a name with a slash": {`{"name":"web/api","role":"runtime"}`, &stubDeploymentQuerier{}, http.StatusBadRequest},
		"a name with a colon": {`{"name":"web:x","role":"runtime"}`, &stubDeploymentQuerier{}, http.StatusBadRequest},
		"no such deployment":  {`{"name":"ghost","role":"runtime"}`, &stubDeploymentQuerier{}, http.StatusNotFound},
		"registry unreadable": {`{"name":"web","role":"runtime"}`, &stubDeploymentQuerier{err: errors.New("down")}, http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			g := chainGateway(t, "acme", &stubKeyDatabase{})
			g.deploymentQuerier = tc.q
			rec := httptest.NewRecorder()
			g.setAppGrant(rec, appGrantPost(tc.body))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
