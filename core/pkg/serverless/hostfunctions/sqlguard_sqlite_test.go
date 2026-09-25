package hostfunctions

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// bugboard #425: the guard has to agree with SQLite about which table a
// statement names. Rather than listing the separators and positions SQLite
// accepts, this asks SQLite: every statement below that SQLite runs against the
// protected table must be refused by the guard. A separator the guard did not
// know (form feed, a \v after a space) or a position it did not track (after a
// comma, inside a parenthesis, after UPDATE OR REPLACE) let a statement through
// that SQLite then ran.
func TestCheckGuestSQL_refusesWhateverSQLiteReadsAsTheTable(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{`CREATE TABLE api_keys (k TEXT)`, `CREATE TABLE messages (id INTEGER, body TEXT)`} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}

	// SEP is replaced by a separator, NAME by a way of writing the table.
	templates := []string{
		"SELECT * FROMSEPNAME",
		"SELECT * FROM messages,SEPNAME",
		"SELECT * FROM (SEPNAME)",
		"UPDATE OR REPLACESEPNAME SET k = 'x'",
		"DELETE FROMSEPNAME",
		"INSERT INTOSEPNAME(k) VALUES ('x')",
	}
	names := []string{"'api_keys'", `"api_keys"`, "`api_keys`", "[api_keys]", "api_keys", "main.'api_keys'"}

	ran := 0
	for b := 0; b < 256; b++ {
		c := string(rune(b))
		if b >= 0x80 {
			c = string([]byte{byte(b)})
		}
		for _, sep := range []string{c, " " + c, c + " "} {
			for _, tmpl := range templates {
				for _, name := range names {
					q := strings.ReplaceAll(strings.ReplaceAll(tmpl, "SEP", sep), "NAME", name)
					if _, err := db.Exec(q); err != nil {
						continue // SQLite does not read this as the table
					}
					ran++
					if checkGuestSQL(q) == nil {
						t.Errorf("SQLite ran %q against api_keys, and the guard let it through", q)
					}
				}
			}
		}
	}
	if ran == 0 {
		t.Fatal("SQLite ran none of the probes; the test is not testing anything")
	}
}
