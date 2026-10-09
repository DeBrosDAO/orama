package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/deployments"
)

// ErrAmbiguousLegacyHost means a host of the legacy {name}.{baseDomain} shape
// names a deployment in more than one namespace. Names are unique per
// namespace, not across them, so the host does not say whose deployment it is;
// the request is refused rather than answered by whichever row the database
// returned first, which would let a namespace take over another's legacy host
// by deploying the same name.
var ErrAmbiguousLegacyHost = errors.New("legacy deployment host names a deployment in more than one namespace")

// deploymentHostColumns is what host routing needs of a deployment row.
const deploymentHostColumns = "id, namespace, name, type, port, content_cid, status, home_node_id, subdomain"

// servingStatuses are the deployment states host routing serves.
const servingStatuses = "('active', 'degraded')"

// legacyHostCandidates is how many rows the legacy lookup reads: one is the
// answer, a second is the ambiguity, and nothing past that changes the verdict.
const legacyHostCandidates = 2

// getDeploymentByDomain looks up the deployment a request host belongs to.
//
// The hosts the platform issues are:
//   - {name}-{random}.{baseDomain}: every deployment created since subdomains
//     were introduced, matched on its subdomain (globally unique).
//   - {name}.{baseDomain}: only a deployment created before they were, which
//     has no subdomain and whose DNS record and URL are its bare name
//     (CreateDNSRecords, BuildDeploymentURLs).
//   - a verified custom domain from deployment_domains.
//
// It returns (nil, nil) when no deployment has the host.
func (g *Gateway) getDeploymentByDomain(ctx context.Context, domain string) (*deployments.Deployment, error) {
	if g.deploymentService == nil {
		return nil, nil
	}
	return lookupDeploymentByHost(client.WithInternalAuth(ctx), g.client.Database(), g.cfg.BaseDomain, domain)
}

// lookupDeploymentByHost resolves host against the registry; see
// getDeploymentByDomain.
func lookupDeploymentByHost(ctx context.Context, db client.DatabaseClient, baseDomain, host string) (*deployments.Deployment, error) {
	host = strings.TrimSuffix(host, ".")

	if label, ok := baseDomainLabel(host, baseDomain); ok {
		deployment, err := lookupBySubdomain(ctx, db, label)
		if err != nil || deployment != nil {
			return deployment, err
		}
		deployment, err = lookupLegacyName(ctx, db, label)
		if err != nil || deployment != nil {
			return deployment, err
		}
	}
	return lookupCustomDomain(ctx, db, host)
}

// baseDomainLabel returns the single label in front of the base domain, and
// whether host has that shape.
func baseDomainLabel(host, baseDomain string) (string, bool) {
	suffix := "." + baseDomain
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	label := strings.TrimSuffix(host, suffix)
	if label == "" || strings.Contains(label, ".") {
		return "", false
	}
	return label, true
}

func lookupBySubdomain(ctx context.Context, db client.DatabaseClient, subdomain string) (*deployments.Deployment, error) {
	result, err := db.Query(ctx, `SELECT `+deploymentHostColumns+` FROM deployments
		WHERE subdomain = ? AND status IN `+servingStatuses+` LIMIT 1`, subdomain)
	if err != nil {
		return nil, fmt.Errorf("failed to look up deployment by subdomain: %w", err)
	}
	if len(result.Rows) == 0 {
		return nil, nil
	}
	return deploymentFromHostRow(result.Rows[0]), nil
}

// lookupLegacyName resolves {name}.{baseDomain} for a deployment that has no
// subdomain. A deployment that has one is reached through it, not through its
// bare name: the bare name was never published for it, and answering it would
// let any namespace that deploys a name serve the host of every other
// namespace's deployment of that name.
func lookupLegacyName(ctx context.Context, db client.DatabaseClient, name string) (*deployments.Deployment, error) {
	result, err := db.Query(ctx, fmt.Sprintf(`SELECT `+deploymentHostColumns+` FROM deployments
		WHERE name = ? AND (subdomain IS NULL OR subdomain = '') AND status IN `+servingStatuses+` LIMIT %d`,
		legacyHostCandidates), name)
	if err != nil {
		return nil, fmt.Errorf("failed to look up deployment by legacy name: %w", err)
	}
	switch len(result.Rows) {
	case 0:
		return nil, nil
	case 1:
		return deploymentFromHostRow(result.Rows[0]), nil
	}
	return nil, fmt.Errorf("%q: %w", name, ErrAmbiguousLegacyHost)
}

func lookupCustomDomain(ctx context.Context, db client.DatabaseClient, domain string) (*deployments.Deployment, error) {
	result, err := db.Query(ctx, `
		SELECT d.id, d.namespace, d.name, d.type, d.port, d.content_cid, d.status, d.home_node_id, d.subdomain
		FROM deployments d
		JOIN deployment_domains dd ON d.id = dd.deployment_id
		WHERE dd.domain = ? AND dd.verified_at IS NOT NULL
		AND d.status IN `+servingStatuses+`
		LIMIT 1`, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to look up deployment by custom domain: %w", err)
	}
	if len(result.Rows) == 0 {
		return nil, nil
	}
	return deploymentFromHostRow(result.Rows[0]), nil
}

// deploymentFromHostRow reads a row selected with deploymentHostColumns.
func deploymentFromHostRow(row []interface{}) *deployments.Deployment {
	return &deployments.Deployment{
		ID:         getString(row[0]),
		Namespace:  getString(row[1]),
		Name:       getString(row[2]),
		Type:       deployments.DeploymentType(getString(row[3])),
		Port:       getInt(row[4]),
		ContentCID: getString(row[5]),
		Status:     deployments.DeploymentStatus(getString(row[6])),
		HomeNodeID: getString(row[7]),
		Subdomain:  getString(row[8]),
	}
}
