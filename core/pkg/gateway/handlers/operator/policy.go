package operator

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Who may create a namespace on this cluster, and how many one wallet may own.
//
// Both are rows in cluster_settings. A missing row is not a licence to guess
// open: a new cluster has no row, and the default is operators. Migration 063
// writes open only when the registry already had data, which is what keeps an
// upgrade from taking creation away from wallets that have it today.

const (
	// SettingNamespaceCreation is who may call POST /v1/namespaces.
	SettingNamespaceCreation = "namespace_creation"
	// SettingMaxNamespacesPerWallet is how many namespaces one wallet may own.
	SettingMaxNamespacesPerWallet = "max_namespaces_per_wallet"

	// CreationOperators allows a wallet that is in the operators table.
	CreationOperators = "operators"
	// CreationAllowlist allows a wallet that is in namespace_creators.
	CreationAllowlist = "allowlist"
	// CreationOpen allows any signed-in wallet.
	CreationOpen = "open"

	// DefaultCreation is what a cluster with no stored setting enforces.
	DefaultCreation = CreationOperators

	// DefaultMaxNamespacesPerWallet is the cap when the cluster has not set
	// one. Each namespace is a cluster of its own — rqlite, Olric, a gateway,
	// a share of the mesh — and there used to be no limit and no cost.
	DefaultMaxNamespacesPerWallet = 10

	// MaxNamespacesPerWalletCeiling is the largest cap an operator can store.
	// The ceiling is what stops a typo from asking the scheduler for millions
	// of namespace clusters.
	MaxNamespacesPerWalletCeiling = 10000
)

// CreationPolicy is the setting the create handler enforces.
type CreationPolicy struct {
	Mode      string
	WalletCap int
}

// PolicyConfigError means a stored setting is not a value this binary can
// enforce. Creation is refused until an operator replaces the row. Treating
// the row as open, or as the default of 10, would silently undo it.
type PolicyConfigError struct {
	Reason string
}

func (e *PolicyConfigError) Error() string {
	if e == nil {
		return ""
	}
	return e.Reason
}

// LoadCreationPolicy reads the two settings. A missing row is the default,
// not an error. A row this binary does not understand is an error.
func LoadCreationPolicy(ctx context.Context, db rqlite.Client) (CreationPolicy, error) {
	if db == nil {
		return CreationPolicy{}, errNoRegistry
	}
	var rows []struct {
		Key   string `db:"key"`
		Value string `db:"value"`
	}
	if err := db.Query(ctx, &rows,
		`SELECT key, value FROM cluster_settings WHERE key IN (?, ?)`,
		SettingNamespaceCreation, SettingMaxNamespacesPerWallet); err != nil {
		return CreationPolicy{}, err
	}

	policy := CreationPolicy{Mode: DefaultCreation, WalletCap: DefaultMaxNamespacesPerWallet}
	for _, row := range rows {
		switch row.Key {
		case SettingNamespaceCreation:
			mode := strings.TrimSpace(row.Value)
			switch mode {
			case CreationOperators, CreationAllowlist, CreationOpen:
				policy.Mode = mode
			default:
				return CreationPolicy{}, &PolicyConfigError{Reason: fmt.Sprintf(
					"namespace_creation is %q, which is not operators, allowlist or open", row.Value)}
			}
		case SettingMaxNamespacesPerWallet:
			n, err := strconv.Atoi(strings.TrimSpace(row.Value))
			if err != nil || n < 1 || n > MaxNamespacesPerWalletCeiling {
				return CreationPolicy{}, &PolicyConfigError{Reason: fmt.Sprintf(
					"max_namespaces_per_wallet is %q, which is not an integer from 1 to %d",
					row.Value, MaxNamespacesPerWalletCeiling)}
			}
			policy.WalletCap = n
		}
	}
	return policy, nil
}

// Permits reports whether wallet may create a namespace under this policy.
//
// An unreadable list is an error, not a denial and not an allow. The caller
// answers 503. Not knowing whether someone is an operator is not permission
// to treat them as one, and it is not a 403 either: a 403 is the policy
// saying no, which a retry would not fix and a registry blip is not.
func (p CreationPolicy) Permits(ctx context.Context, db rqlite.Client, wallet string) (bool, error) {
	wallet = auth.NormalizeWallet(wallet)
	if wallet == "" {
		return false, nil
	}
	switch p.Mode {
	case CreationOpen:
		return true, nil
	case CreationOperators:
		return IsOperator(ctx, db, wallet)
	case CreationAllowlist:
		return isCreator(ctx, db, wallet)
	default:
		return false, &PolicyConfigError{Reason: fmt.Sprintf(
			"namespace_creation is %q, which is not operators, allowlist or open", p.Mode)}
	}
}

func isCreator(ctx context.Context, db rqlite.Client, wallet string) (bool, error) {
	if db == nil {
		return false, errNoRegistry
	}
	var rows []struct {
		Wallet string `db:"wallet"`
	}
	if err := db.Query(ctx, &rows,
		`SELECT wallet FROM namespace_creators WHERE LOWER(wallet) = ? LIMIT 1`, wallet); err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}
