package deployments

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

// testEnvCodec is the environment codec these tests use. The service refuses
// to build without one, because a deployment environment is where the tenant's
// secrets are.
func testEnvCodec() *deployments.EnvCodec {
	codec, err := deployments.NewEnvCodec("test-cluster-secret")
	if err != nil {
		panic(err)
	}
	return codec
}

// newTestService creates a DeploymentService with a no-op rqlite mock and the
// given base domain. Only pure/in-memory methods are exercised by these tests,
// so the DB mock never needs to return real data.
func newTestService(baseDomain string) *DeploymentService {
	return NewDeploymentService(
		&mockRQLiteClient{}, // satisfies rqlite.Client; no DB calls expected
		nil,                 // homeNodeManager — unused by tested methods
		nil,                 // portAllocator   — unused by tested methods
		nil,                 // replicaManager  — unused by tested methods
		zap.NewNop(),        // silent logger
		baseDomain,
		testEnvCodec(),
		nil, // audit — these tests do not record
	)
}

// ---------------------------------------------------------------------------
// BaseDomain
// ---------------------------------------------------------------------------

func TestBaseDomain_ReturnsConfiguredDomain(t *testing.T) {
	svc := newTestService("dbrs.space")

	got := svc.BaseDomain()
	if got != "dbrs.space" {
		t.Fatalf("BaseDomain() = %q, want %q", got, "dbrs.space")
	}
}

func TestBaseDomain_ReturnsEmptyWhenNotConfigured(t *testing.T) {
	svc := newTestService("")

	got := svc.BaseDomain()
	if got != "" {
		t.Fatalf("BaseDomain() = %q, want empty string", got)
	}
}

// ---------------------------------------------------------------------------
// SetBaseDomain
// ---------------------------------------------------------------------------

func TestSetBaseDomain_SetsDomainWhenNonEmpty(t *testing.T) {
	svc := newTestService("")

	svc.SetBaseDomain("example.com")
	got := svc.BaseDomain()
	if got != "example.com" {
		t.Fatalf("after SetBaseDomain(\"example.com\"), BaseDomain() = %q, want %q", got, "example.com")
	}
}

func TestSetBaseDomain_OverwritesExistingDomain(t *testing.T) {
	svc := newTestService("old.domain")

	svc.SetBaseDomain("new.domain")
	got := svc.BaseDomain()
	if got != "new.domain" {
		t.Fatalf("after SetBaseDomain(\"new.domain\"), BaseDomain() = %q, want %q", got, "new.domain")
	}
}

func TestSetBaseDomain_DoesNotOverwriteWithEmptyString(t *testing.T) {
	svc := newTestService("keep.me")

	svc.SetBaseDomain("")
	got := svc.BaseDomain()
	if got != "keep.me" {
		t.Fatalf("after SetBaseDomain(\"\"), BaseDomain() = %q, want %q (should not overwrite)", got, "keep.me")
	}
}

// ---------------------------------------------------------------------------
// SetNodePeerID
// ---------------------------------------------------------------------------

func TestSetNodePeerID_SetsPeerIDCorrectly(t *testing.T) {
	svc := newTestService("dbrs.space")

	svc.SetNodePeerID("12D3KooWGqyuQR8Nxyz1234567890abcdef")
	if svc.nodePeerID != "12D3KooWGqyuQR8Nxyz1234567890abcdef" {
		t.Fatalf("nodePeerID = %q, want %q", svc.nodePeerID, "12D3KooWGqyuQR8Nxyz1234567890abcdef")
	}
}

func TestSetNodePeerID_OverwritesPreviousValue(t *testing.T) {
	svc := newTestService("dbrs.space")

	svc.SetNodePeerID("first-peer-id")
	svc.SetNodePeerID("second-peer-id")
	if svc.nodePeerID != "second-peer-id" {
		t.Fatalf("nodePeerID = %q, want %q", svc.nodePeerID, "second-peer-id")
	}
}

func TestSetNodePeerID_AcceptsEmptyString(t *testing.T) {
	svc := newTestService("dbrs.space")

	svc.SetNodePeerID("some-peer")
	svc.SetNodePeerID("")
	if svc.nodePeerID != "" {
		t.Fatalf("nodePeerID = %q, want empty string", svc.nodePeerID)
	}
}

// ---------------------------------------------------------------------------
// BuildDeploymentURLs
// ---------------------------------------------------------------------------

func TestBuildDeploymentURLs_UsesSubdomainIfSet(t *testing.T) {
	svc := newTestService("dbrs.space")

	dep := &deployments.Deployment{
		Name:      "myapp",
		Subdomain: "myapp-f3o4if",
	}

	urls := svc.BuildDeploymentURLs(dep)
	if len(urls) != 1 {
		t.Fatalf("BuildDeploymentURLs() returned %d URLs, want 1", len(urls))
	}

	want := "https://myapp-f3o4if.dbrs.space"
	if urls[0] != want {
		t.Fatalf("BuildDeploymentURLs() = %q, want %q", urls[0], want)
	}
}

func TestBuildDeploymentURLs_FallsBackToNameIfSubdomainEmpty(t *testing.T) {
	svc := newTestService("dbrs.space")

	dep := &deployments.Deployment{
		Name:      "myapp",
		Subdomain: "",
	}

	urls := svc.BuildDeploymentURLs(dep)
	if len(urls) != 1 {
		t.Fatalf("BuildDeploymentURLs() returned %d URLs, want 1", len(urls))
	}

	want := "https://myapp.dbrs.space"
	if urls[0] != want {
		t.Fatalf("BuildDeploymentURLs() = %q, want %q", urls[0], want)
	}
}

func TestBuildDeploymentURLs_ConstructsCorrectURLWithBaseDomain(t *testing.T) {
	tests := []struct {
		name       string
		baseDomain string
		subdomain  string
		depName    string
		wantURL    string
	}{
		{
			name:       "standard domain with subdomain",
			baseDomain: "example.com",
			subdomain:  "app-abc123",
			depName:    "app",
			wantURL:    "https://app-abc123.example.com",
		},
		{
			name:       "standard domain without subdomain",
			baseDomain: "example.com",
			subdomain:  "",
			depName:    "my-service",
			wantURL:    "https://my-service.example.com",
		},
		{
			name:       "nested base domain",
			baseDomain: "apps.staging.example.com",
			subdomain:  "frontend-x1y2z3",
			depName:    "frontend",
			wantURL:    "https://frontend-x1y2z3.apps.staging.example.com",
		},
		{
			name:       "empty base domain",
			baseDomain: "",
			subdomain:  "myapp-abc123",
			depName:    "myapp",
			wantURL:    "https://myapp-abc123.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(tt.baseDomain)

			dep := &deployments.Deployment{
				Name:      tt.depName,
				Subdomain: tt.subdomain,
			}

			urls := svc.BuildDeploymentURLs(dep)
			if len(urls) != 1 {
				t.Fatalf("BuildDeploymentURLs() returned %d URLs, want 1", len(urls))
			}
			if urls[0] != tt.wantURL {
				t.Fatalf("BuildDeploymentURLs() = %q, want %q", urls[0], tt.wantURL)
			}
		})
	}
}
