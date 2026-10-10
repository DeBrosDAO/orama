package nodenames

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// What a pass reads and decides: the rows other writers own, the rows this package owns, and the
// difference between them and the records wanted.

// foreignNames is the set of names below zone that a record of another owner already answers for.
// A claimed name must not add an address next to a host the cluster itself publishes, so such a
// name is not published.
func foreignNames(ctx context.Context, db *sql.DB, zone string) (map[string]struct{}, error) {
	rows, err := rqlite.SafeQueryContext(db, ctx, selectForeignSQL, RecordNamespace, "%."+zone+".")
	if err != nil {
		return nil, fmt.Errorf("read the names below %s that other writers own: %w", zone, err)
	}
	defer rows.Close()
	foreign := map[string]struct{}{}
	for rows.Next() {
		var fqdn string
		if err := rows.Scan(&fqdn); err != nil {
			return nil, fmt.Errorf("read a name that another writer owns: %w", err)
		}
		foreign[fqdn] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("finish reading the names below %s that other writers own: %w", zone, err)
	}
	return foreign, nil
}

// dropForeign removes the records of names that another owner holds, and reports each such name.
func dropForeign(want []Record, foreign map[string]struct{}) ([]Record, []Refusal) {
	var kept []Record
	var refused []Refusal
	reported := map[string]struct{}{}
	for _, r := range want {
		if _, taken := foreign[r.FQDN]; !taken {
			kept = append(kept, r)
			continue
		}
		if _, done := reported[r.FQDN]; !done {
			reported[r.FQDN] = struct{}{}
			refused = append(refused, Refusal{Name: r.FQDN, Reason: foreignRefusal})
		}
	}
	return kept, refused
}

// owned reads the rows this package owns, with whether each is active. is_active comes back as a
// bool from SQLite's driver and as a JSON number (float64) from the rqlite driver, and database/sql
// converts neither to the other, so it is scanned as it comes and read by truthy.
func owned(ctx context.Context, db *sql.DB) (map[Record]bool, error) {
	rows, err := rqlite.SafeQueryContext(db, ctx, selectOwnedSQL, RecordNamespace)
	if err != nil {
		return nil, fmt.Errorf("read the node-name records in dns_records: %w", err)
	}
	defer rows.Close()
	have := map[Record]bool{}
	for rows.Next() {
		var r Record
		var active any
		if err := rows.Scan(&r.FQDN, &r.Type, &r.Value, &active); err != nil {
			return nil, fmt.Errorf("read a node-name record: %w", err)
		}
		on, err := truthy(active)
		if err != nil {
			return nil, fmt.Errorf("read the is_active of %s %s %s: %w", r.FQDN, r.Type, r.Value, err)
		}
		have[r] = on
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("finish reading the node-name records in dns_records: %w", err)
	}
	return have, nil
}

// truthy reads a boolean column the way either driver returns it.
func truthy(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case int64:
		return x != 0, nil
	case float64:
		return x != 0, nil
	case []byte:
		return truthy(string(x))
	case string:
		switch x {
		case "1", "true", "TRUE":
			return true, nil
		case "0", "false", "FALSE":
			return false, nil
		}
	}
	return false, fmt.Errorf("%v (%T) is not a boolean", v, v)
}

// diff is what must be added, reactivated and removed to make have equal want. Each list is sorted,
// so every node of the cluster does the same writes in the same order.
func diff(want []Record, have map[Record]bool) plan {
	var p plan
	wanted := make(map[Record]struct{}, len(want))
	for _, r := range want {
		wanted[r] = struct{}{}
		if active, present := have[r]; !present {
			p.add = append(p.add, r)
		} else if !active {
			p.activate = append(p.activate, r)
		}
	}
	for r := range have {
		if _, keep := wanted[r]; !keep {
			p.remove = append(p.remove, r)
		}
	}
	sortRecords(p.remove)
	return p
}

func sortRecords(rs []Record) {
	sort.Slice(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.FQDN != b.FQDN {
			return a.FQDN < b.FQDN
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Value < b.Value
	})
}

// boundRemovals returns the removals this pass may make and how many it holds back. While the chain
// node is catching up none is made; otherwise at most the larger of MinRemovalsPerPass and the
// MaxRemovalsNumerator/MaxRemovalsDenominator share of the rows held.
func boundRemovals(remove []Record, held int, catchingUp bool) ([]Record, int) {
	if catchingUp {
		return nil, len(remove)
	}
	allowed := max(MinRemovalsPerPass, held*MaxRemovalsNumerator/MaxRemovalsDenominator)
	if len(remove) <= allowed {
		return remove, 0
	}
	return remove[:allowed], len(remove) - allowed
}
