package setup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/tlsstatus"
)

// CertificateStatus runs `orama node tls status --json` on the machine: which
// names its Caddy holds a certificate for, and what the certificate authority
// last answered to the ones it does not.
func (m *sshMachine) CertificateStatus(ctx context.Context) (*tlsstatus.Report, error) {
	out, err := m.capture(ctx, m.sudo()+orama+" node tls status --json", nil)
	if err != nil {
		return nil, fmt.Errorf("orama node tls status on %s: %w", m.node.Host, err)
	}
	var report tlsstatus.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		return nil, fmt.Errorf("orama node tls status on %s did not print a report: %w (%.80q)", m.node.Host, err, out)
	}
	return &report, nil
}
