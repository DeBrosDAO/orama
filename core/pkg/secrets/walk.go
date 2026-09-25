package secrets

import (
	"context"
	"fmt"
	"strings"
)

// WalkDB is the RQLite handle the walker writes through.
type WalkDB = Store

// Column is one ciphertext column to rewrite.
type Column struct {
	Table   string
	Column  string
	IDCols  []string
	Purpose string
}

// NamespaceColumns are the tenant-DB columns sealed with the encryption root.
func NamespaceColumns() []Column {
	return []Column{
		{Table: "function_secrets", Column: "encrypted_value", IDCols: []string{"id"}, Purpose: "orama-secrets-encryption-v1"},
		{Table: "push_devices", Column: "token_encrypted", IDCols: []string{"id"}, Purpose: "push-device-tokens"},
		{Table: "push_topics", Column: "token_encrypted", IDCols: []string{"namespace", "topic_id"}, Purpose: "push-topic-tokens"},
		{Table: "namespace_push_config", Column: "ntfy_auth_token_encrypted", IDCols: []string{"namespace"}, Purpose: "namespace-push-config"},
		{Table: "namespace_push_config", Column: "expo_access_token_encrypted", IDCols: []string{"namespace"}, Purpose: "namespace-push-config"},
		{Table: "namespace_push_credentials", Column: "credentials_json", IDCols: []string{"namespace", "provider"}, Purpose: "namespace-push-credentials"},
		{Table: "namespace_webrtc_config", Column: "turn_shared_secret", IDCols: []string{"id"}, Purpose: "turn-encryption"},
		{Table: "deployments", Column: "environment", IDCols: []string{"id"}, Purpose: "orama-deployment-environment-v1"},
	}
}

// IndexColumns are the registry columns sealed with the encryption root.
func IndexColumns() []Column {
	return []Column{
		{Table: "wireguard_peers", Column: "agent_token", IDCols: []string{"node_id"}, Purpose: "node-agent-token"},
	}
}

// WalkResult is how many rows the walker looked at and rewrote.
type WalkResult struct {
	Scanned  int      `json:"scanned"`
	Rewrote  int      `json:"rewrote"`
	Skipped  int      `json:"skipped"`
	Missing  int      `json:"missing"`
	Failures []string `json:"failures,omitempty"`
}

// Walk re-encrypts every listed column under root. It is idempotent: a row
// already sealed with the current key id is left alone. Unprefixed leftover
// plaintext (deployment env, TURN) is sealed rather than refused, which is
// the migration those columns still need.
func Walk(ctx context.Context, db WalkDB, root Root, cols []Column) (WalkResult, error) {
	var out WalkResult
	if db == nil {
		return out, fmt.Errorf("re-encrypt: no database")
	}
	for _, col := range cols {
		r, err := walkColumn(ctx, db, root, col)
		out.Scanned += r.Scanned
		out.Rewrote += r.Rewrote
		out.Skipped += r.Skipped
		out.Missing += r.Missing
		out.Failures = append(out.Failures, r.Failures...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func walkColumn(ctx context.Context, db WalkDB, root Root, col Column) (WalkResult, error) {
	var out WalkResult
	ks, err := root.Keyset(col.Purpose)
	if err != nil {
		return out, err
	}

	selectCols := strings.Join(append(append([]string{}, col.IDCols...), col.Column), ", ")
	query := fmt.Sprintf("SELECT %s FROM %s", selectCols, col.Table)

	var rows []map[string]any
	if err := db.Query(ctx, &rows, query); err != nil {
		if isNoSuchTable(err) {
			out.Missing = 1
			return out, nil
		}
		return out, fmt.Errorf("re-encrypt %s.%s: %w", col.Table, col.Column, err)
	}

	for _, row := range rows {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		raw := stringify(row[col.Column])
		if strings.TrimSpace(raw) == "" {
			out.Skipped++
			continue
		}
		out.Scanned++

		if alreadyOnTarget(ks, raw) {
			out.Skipped++
			continue
		}
		plain, err := decryptForWalk(ks, raw)
		if err != nil {
			out.Failures = append(out.Failures, fmt.Sprintf("%s.%s %v: %v", col.Table, col.Column, idsOf(row, col.IDCols), err))
			continue
		}
		sealed, err := ks.Encrypt(plain)
		if err != nil {
			out.Failures = append(out.Failures, fmt.Sprintf("%s.%s %v: %v", col.Table, col.Column, idsOf(row, col.IDCols), err))
			continue
		}
		set := fmt.Sprintf("UPDATE %s SET %s = ?", col.Table, col.Column)
		where, args := whereIDs(col.IDCols, row)
		args = append([]any{sealed}, args...)
		if _, err := db.Exec(ctx, set+" WHERE "+where, args...); err != nil {
			out.Failures = append(out.Failures, fmt.Sprintf("%s.%s %v: %v", col.Table, col.Column, idsOf(row, col.IDCols), err))
			continue
		}
		out.Rewrote++
	}
	return out, nil
}

func alreadyOnTarget(ks Keyset, raw string) bool {
	env, err := ParseEnvelope(raw)
	if err != nil {
		return false
	}
	if ks.WriteVersioned {
		return env.Version == 1 && env.KeyID == ks.CurrentID
	}
	return env.Version == 0
}

func decryptForWalk(ks Keyset, raw string) (string, error) {
	if !IsEncrypted(raw) {
		return raw, nil
	}
	return ks.Decrypt(raw)
}

func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}

func idsOf(row map[string]any, cols []string) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, stringify(row[c]))
	}
	return out
}

func whereIDs(cols []string, row map[string]any) (string, []any) {
	parts := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	for _, c := range cols {
		parts = append(parts, c+" = ?")
		args = append(args, stringify(row[c]))
	}
	return strings.Join(parts, " AND "), args
}

func isNoSuchTable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no such table")
}
