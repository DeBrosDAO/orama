package sqlite

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/mattn/go-sqlite3"
)

const tenantDriver = "sqlite3_tenant_noattach"

var tenantDriverOnce sync.Once

var attachWord = regexp.MustCompile(`(?i)\b(ATTACH|DETACH)\b`)
var sqlStringLit = regexp.MustCompile(`'([^']|'')*'|"([^"]|"")*"`)

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

// rejectCrossDBSQL blocks ATTACH/DETACH and extra statements. Tenant SQL is
// allowed against one file; multi-statement + ATTACH is the cross-namespace
// escape (bugboard #252).
func rejectCrossDBSQL(query string) error {
	trimmed := strings.TrimSpace(query)
	for strings.HasSuffix(trimmed, ";") {
		trimmed = strings.TrimSpace(trimmed[:len(trimmed)-1])
	}
	if trimmed == "" {
		return fmt.Errorf("empty query")
	}
	if strings.Contains(trimmed, ";") {
		return fmt.Errorf("multiple SQL statements are not allowed")
	}
	if attachWord.MatchString(sqlStringLit.ReplaceAllString(trimmed, " ")) {
		return fmt.Errorf("ATTACH/DETACH is not allowed")
	}
	return nil
}
