package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"orama-demo/host"
)

// book is a guestbook table the Fake database answers for: the three statements
// the function issues, and nothing else.
type book struct {
	rows     []map[string]any
	nextID   int64
	creates  int
	execFail error
	lastArgs []any
}

func (b *book) attach(f *host.Fake) {
	f.OnExec = func(sql string, args []any) (host.Result, error) {
		if b.execFail != nil {
			return host.Result{}, b.execFail
		}
		switch {
		case strings.HasPrefix(sql, "CREATE TABLE IF NOT EXISTS guestbook_entries"):
			b.creates++
			return host.Result{}, nil
		case strings.HasPrefix(sql, "INSERT INTO guestbook_entries"):
			b.nextID++
			b.lastArgs = args
			b.rows = append(b.rows, map[string]any{"id": float64(b.nextID), "name": args[0], "message": args[1], "created_at": "2026-10-10 12:00:00"})
			return host.Result{RowsAffected: 1, LastInsertID: b.nextID}, nil
		}
		return host.Result{}, errors.New("unexpected statement: " + sql)
	}
	f.OnQuery = func(sql string, args []any) ([]map[string]any, error) {
		if !strings.HasPrefix(sql, "SELECT id, name, message, created_at FROM guestbook_entries") {
			return nil, errors.New("unexpected query: " + sql)
		}
		limit := int(args[0].(int))
		var out []map[string]any
		for i := len(b.rows) - 1; i >= 0 && len(out) < limit; i-- {
			out = append(out, b.rows[i])
		}
		return out, nil
	}
}

func newBook() (*host.Fake, *book) {
	f, b := host.NewFake(), &book{}
	b.attach(f)
	return f, b
}

func ask(t *testing.T, h host.Host, input string) response {
	t.Helper()
	out, err := handle(h, []byte(input))
	if err != nil {
		t.Fatalf("handle(%s): %v", input, err)
	}
	var got response
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output %s is not a guestbook: %v", out, err)
	}
	return got
}

func TestHandle_signingAddsTheEntryAndReturnsTheBookNewestFirst(t *testing.T) {
	f, b := newBook()
	f.Wallet = "0xabc"

	ask(t, f, `{"action":"sign","name":"Ada","message":"first"}`)
	got := ask(t, f, `{"action":"sign","name":"Grace","message":"second"}`)

	if len(got.Entries) != 2 || got.Entries[0].Name != "Grace" || got.Entries[1].Name != "Ada" {
		t.Errorf("entries = %+v, want Grace then Ada", got.Entries)
	}
	if got.Signed == nil || got.Signed.ID != 2 || got.Signed.At == "" {
		t.Errorf("signed = %+v, want the entry just written with its time", got.Signed)
	}
	if b.lastArgs[2] != "0xabc" {
		t.Errorf("the entry recorded caller %v, want the wallet that signed", b.lastArgs[2])
	}
}

func TestHandle_listReadsWithoutWriting(t *testing.T) {
	f, b := newBook()
	ask(t, f, `{"action":"sign","name":"Ada","message":"hi"}`)
	before := b.nextID

	got := ask(t, f, `{"action":"list"}`)
	again := ask(t, f, ``)

	if len(got.Entries) != 1 || len(again.Entries) != 1 || b.nextID != before || got.Signed != nil {
		t.Errorf("list changed the book or answered %+v / %+v", got, again)
	}
}

func TestHandle_anEmptyBookIsAnEmptyListNotNull(t *testing.T) {
	f, _ := newBook()

	out, err := handle(f, []byte(`{}`))

	if err != nil || !strings.Contains(string(out), `"entries":[]`) {
		t.Errorf("output %s, err %v: an empty book must serialise as [] for the page to loop over", out, err)
	}
}

func TestHandle_theBookHoldsTheNewestTwenty(t *testing.T) {
	f, _ := newBook()
	for i := 0; i < 25; i++ {
		ask(t, f, `{"action":"sign","name":"n","message":"m"}`)
	}

	got := ask(t, f, `{}`)

	if len(got.Entries) != listLimit || got.Entries[0].ID != 25 {
		t.Errorf("%d entries, newest %d; want %d, newest 25", len(got.Entries), got.Entries[0].ID, listLimit)
	}
}

func TestHandle_signingNeedsANameAndAMessage(t *testing.T) {
	for _, in := range []string{`{"action":"sign"}`, `{"action":"sign","name":"Ada"}`, `{"action":"sign","message":"hi"}`, `{"action":"sign","name":"  ","message":"\u0007"}`} {
		f, b := newBook()

		out, err := handle(f, []byte(in))

		if err != nil || !strings.Contains(string(out), `"error"`) || len(b.rows) != 0 {
			t.Errorf("input %s: output %s, err %v, rows %d; want a refusal that wrote nothing", in, out, err, len(b.rows))
		}
	}
}

func TestHandle_longEntriesAreCutAndControlCharactersDropped(t *testing.T) {
	f, b := newBook()
	long := strings.Repeat("x", 1000)

	ask(t, f, `{"action":"sign","name":"`+long+`","message":"`+long+`\u001b[31m\n"}`)

	if n := len([]rune(b.rows[0]["name"].(string))); n != maxNameRunes {
		t.Errorf("name of %d characters stored, want %d", n, maxNameRunes)
	}
	msg := b.rows[0]["message"].(string)
	if len([]rune(msg)) != maxMessageRunes || strings.ContainsAny(msg, "\x1b\n") {
		t.Errorf("message of %d characters stored, or it kept control characters", len([]rune(msg)))
	}
}

func TestHandle_anEntryIsABoundArgumentNeverSQLText(t *testing.T) {
	f, b := newBook()

	ask(t, f, `{"action":"sign","name":"x'); DROP TABLE t;--","message":"x"}`)

	if b.rows[0]["name"] != "x'); DROP TABLE t;--" {
		t.Errorf("the name was not stored as data: %v", b.rows[0]["name"])
	}
}

func TestHandle_theTableIsMadeBeforeEveryUseSoAFreshNamespaceWorks(t *testing.T) {
	f, b := newBook()

	ask(t, f, `{}`)

	if b.creates != 1 {
		t.Errorf("CREATE TABLE IF NOT EXISTS ran %d times on a read, want 1", b.creates)
	}
}

func TestHandle_aDatabaseFailureIsAFailureOfTheFunction(t *testing.T) {
	f, b := newBook()
	b.execFail = errors.New("database unavailable")

	for _, in := range []string{`{}`, `{"action":"sign","name":"Ada","message":"hi"}`} {
		if _, err := handle(f, []byte(in)); err == nil || !strings.Contains(err.Error(), "database unavailable") {
			t.Errorf("input %s: err = %v, want the database's reason", in, err)
		}
	}
}

func TestHandle_aHostWithoutADatabaseSaysSo(t *testing.T) {
	if _, err := handle(host.NewFake(), []byte(`{}`)); !errors.Is(err, host.ErrNoDatabase) {
		t.Errorf("err = %v, want ErrNoDatabase", err)
	}
}

func TestHandle_anUnknownActionAndBadJSONAreAnswers(t *testing.T) {
	for _, in := range []string{`{"action":"delete"}`, `not json`} {
		f, _ := newBook()
		if out, err := handle(f, []byte(in)); err != nil || !strings.Contains(string(out), `"error"`) {
			t.Errorf("input %s: output %s, err %v", in, out, err)
		}
	}
}
