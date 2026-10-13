package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/sqlguard"
	"github.com/mattn/go-sqlite3"
)

const tenantDriver = "sqlite3_tenant_noattach"

var tenantDriverOnce sync.Once

func registerTenantDriver() {
	tenantDriverOnce.Do(func() {
		sql.Register(tenantDriver, &sqlite3.SQLiteDriver{
			ConnectHook: func(conn *sqlite3.SQLiteConn) error {
				conn.SetLimit(sqlite3.SQLITE_LIMIT_ATTACHED, 0)
				return nil
			},
		})
	})
}

func openTenantDB(path string) (*sql.DB, error) {
	registerTenantDriver()
	return sql.Open(tenantDriver, path)
}

// rejectCrossDBSQL blocks statements that reach outside the tenant's own file:
// ATTACH/DETACH, VACUUM INTO and extra statements (bugboard #252). The check is
// over SQL tokens (sqlguard.CheckTenantSQLite), not the query text.
func rejectCrossDBSQL(query string) error {
	if strings.Trim(query, " \t\r\n\f\v;") == "" {
		return fmt.Errorf("empty query")
	}
	return sqlguard.CheckTenantSQLite(query)
}
