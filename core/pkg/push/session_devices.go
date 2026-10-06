package push

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// A push registration made from a device-bound session belongs to that device.
//
// Revoking the device and ending its push registrations are one fact: a
// registration whose session device is revoked is never listed and never sent
// to, whatever gateway revoked the device and whether or not its row has been
// removed yet. Rows are removed the first time they are found — here, in the
// namespace's own database, which is where they live.

// SessionDeviceGate reports which of ids are revoked session devices. It is the
// auth service's answer, read from the cluster registry.
type SessionDeviceGate func(ctx context.Context, namespace string, ids []string) (map[string]bool, error)

// SetSessionDeviceGate wires the answer to "which session devices are revoked".
// Without one, a device-bound registration cannot be checked and listing it
// fails rather than waking a device that may have been revoked.
func (s *RqliteDeviceStore) SetSessionDeviceGate(gate SessionDeviceGate) {
	if s != nil {
		s.revokedDevices = gate
	}
}

// dropRevokedDeviceRows removes the registrations of revoked session devices
// from rows, and from the table.
func (s *RqliteDeviceStore) dropRevokedDeviceRows(ctx context.Context, namespace string, rows []deviceRow) ([]deviceRow, error) {
	var ids []string
	seen := map[string]bool{}
	for _, r := range rows {
		if r.SessionDeviceID != "" && !seen[r.SessionDeviceID] {
			seen[r.SessionDeviceID] = true
			ids = append(ids, r.SessionDeviceID)
		}
	}
	if len(ids) == 0 {
		return rows, nil
	}
	if s.revokedDevices == nil {
		return nil, fmt.Errorf("push: %d registration(s) are bound to session devices and this gateway "+
			"cannot check whether those devices were revoked", len(ids))
	}
	revoked, err := s.revokedDevices(ctx, namespace, ids)
	if err != nil {
		return nil, fmt.Errorf("push: check which session devices are revoked: %w", err)
	}
	if len(revoked) == 0 {
		return rows, nil
	}

	kept := rows[:0]
	for _, r := range rows {
		if !revoked[r.SessionDeviceID] {
			kept = append(kept, r)
		}
	}
	for id := range revoked {
		if err := s.DeleteForSessionDevice(ctx, namespace, id); err != nil {
			return nil, err
		}
	}
	return kept, nil
}

// DeleteForSessionDevice removes every registration a session device made in a
// namespace.
func (s *RqliteDeviceStore) DeleteForSessionDevice(ctx context.Context, namespace, sessionDeviceID string) error {
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(sessionDeviceID) == "" {
		return fmt.Errorf("namespace and session device id required")
	}
	res, err := s.db.Exec(ctx,
		`DELETE FROM push_devices WHERE namespace = ? AND session_device_id = ?`, namespace, sessionDeviceID)
	if err != nil {
		return fmt.Errorf("remove the push registrations of revoked device %s: %w", sessionDeviceID, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.logger.Info("removed the push registrations of a revoked session device",
			zap.String("namespace", namespace), zap.Int64("removed", n))
	}
	return nil
}

// nullableString stores "" as NULL, so an unbound registration has no session
// device rather than an empty one.
func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
