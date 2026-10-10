package webrtc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Migrations 073 and 075 create its tables IF NOT EXISTS. A namespace whose own
// database already had a table of the same name (a tenant's, from before the
// platform used the name) keeps it, and every query against it would then fail
// with an error that does not say why, or worse, read the tenant's rows as
// admissions. The store checks the tables once, before first use, and refuses
// with an error that names the table and what is wrong with it.

// generationColumn is the column migration 075 adds to webrtc_admissions.
const generationColumn = "generation"

// errAdmissionSchema marks a refusal because a table is not the one migration 073
// makes. The tables are checked again on the next join, so a namespace that
// fixes its database is admitted without a restart; until it does, every join
// is refused.
var errAdmissionSchema = errors.New("the namespace's WebRTC admission tables are unusable")

// tableShape is what a table must have: the columns, and which of them make up
// the primary key (the upserts name it in ON CONFLICT).
type tableShape struct {
	name    string
	columns []string
	key     []string
}

var admissionTables = []tableShape{
	{
		name:    "webrtc_settings",
		columns: []string{"namespace", "require_admission", "updated_at"},
		key:     []string{"namespace"},
	},
	{
		name:    "webrtc_admissions",
		columns: []string{"namespace", "room", "user_id", "device_id", "expires_at", "revoked_at", "muted", "created_at", generationColumn},
		key:     []string{"namespace", "room", "user_id", "device_id"},
	},
}

type columnInfo struct {
	Name string `db:"name"`
	Pk   int    `db:"pk"`
}

// ensureSchema verifies the tables the first time it is called and again after
// a failure, so a namespace that fixes its table is admitted without a restart.
func (s *AdmissionStore) ensureSchema(ctx context.Context) error {
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	if s.schemaOK {
		return nil
	}
	for _, want := range admissionTables {
		var cols []columnInfo
		if err := s.db.Query(ctx, &cols, `SELECT name, pk FROM pragma_table_info('`+want.name+`')`); err != nil {
			return fmt.Errorf("failed to read the schema of table %s: %w", want.name, err)
		}
		if len(cols) == 0 {
			// Not a mismatch: the migration may simply not have reached this
			// database yet, and a retry can find it.
			return fmt.Errorf("table %s does not exist: are migrations 073 and 075 applied to this namespace's database?", want.name)
		}
		if err := want.check(cols); err != nil {
			return fmt.Errorf("%w: %v", errAdmissionSchema, err)
		}
	}
	s.schemaOK = true
	return nil
}

// check compares the table's columns with the shape.
func (t tableShape) check(have []columnInfo) error {
	present, key := map[string]bool{}, map[string]bool{}
	for _, c := range have {
		present[c.Name] = true
		if c.Pk > 0 {
			key[c.Name] = true
		}
	}
	var missing []string
	for _, c := range t.columns {
		if !present[c] {
			missing = append(missing, c)
		}
	}
	onlyGeneration := len(missing) == 1 && missing[0] == generationColumn
	if len(missing) > 0 && !onlyGeneration {
		sort.Strings(missing)
		return fmt.Errorf("table %s in this namespace's database is not the WebRTC admission table: it lacks columns %s; "+
			"it predates migration 073 (and 075, which do not replace an existing table), so rename or drop it and re-apply the migrations",
			t.name, strings.Join(missing, ", "))
	}
	if len(key) != len(t.key) {
		return fmt.Errorf("table %s has primary key columns %s, want exactly %s; rename or drop it and re-apply migration 073 and 075",
			t.name, keyNames(key), strings.Join(t.key, ", "))
	}
	for _, k := range t.key {
		if !key[k] {
			return fmt.Errorf("table %s has primary key columns %s, want exactly %s; rename or drop it and re-apply migration 073 and 075",
				t.name, keyNames(key), strings.Join(t.key, ", "))
		}
	}
	if onlyGeneration {
		// The platform's own table, made by 073, before 075 added the column:
		// the namespace gateway applies 075 when it starts.
		return fmt.Errorf("table %s has no %s column: migration 075 has not been applied to this namespace's database yet; "+
			"its gateway applies it when it starts", t.name, generationColumn)
	}
	return nil
}

func keyNames(key map[string]bool) string {
	names := make([]string, 0, len(key))
	for k := range key {
		names = append(names, k)
	}
	sort.Strings(names)
	return "(" + strings.Join(names, ", ") + ")"
}
