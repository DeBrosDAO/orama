package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeJWT and fakeKey are distinct values of the shapes the patterns mask.
func fakeJWT(i int) string {
	return fmt.Sprintf("eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ%06d.c2lnbmF0dXJl%06d", i, i)
}
func fakeKey(i int) string { return fmt.Sprintf("orama_live_%08dpayload_chk%04d", i, i%10000) }

// fakeRefresh is a distinct value of a refresh token's shape: 43 base64url
// characters, after "dv1_" when the session is device bound.
func fakeRefresh(i int, deviceBound bool) string {
	v := fmt.Sprintf("rT-%010d_%029d", i, i)
	if deviceBound {
		return "dv1_" + v
	}
	return v
}

// Every sign-in mints a refresh token. The stagenet run of 2026-10-10 held
// 14,353 of them as ordinary literals, reached MaxValues in stage 11, and then
// every sign-in failed to register its credentials: refresh tokens are tokens.
func TestAdd_refreshTokensTakeNoLiteralSlot(t *testing.T) {
	r := NewRedactor()
	var minted []string
	for i := 0; i < MaxValues+10; i++ {
		minted = append(minted, fakeRefresh(i, i%2 == 0))
	}
	if err := r.Add(minted...); err != nil {
		t.Fatalf("Add of %d refresh tokens: %v", len(minted), err)
	}
	if err := r.Add("a-literal-credential-1234"); err != nil {
		t.Fatalf("a literal value after them was refused: %v", err)
	}
	out := r.Redact("refresh " + fakeRefresh(9, false) + " bound " + fakeRefresh(8, true))
	if strings.Contains(out, "rT-") {
		t.Errorf("a registered refresh token survived redaction: %s", out)
	}
}

// A run resumed across deploys minted past MaxValues, almost all of them
// tokens and keys, and every line the runner printed was withheld: values the
// shape patterns mask take no slot.
func TestAdd_shapeMaskedValuesTakeNoSlot(t *testing.T) {
	r := NewRedactor()
	var minted []string
	for i := 0; i < MaxValues+10; i++ {
		minted = append(minted, fakeJWT(i), fakeKey(i))
	}
	if err := r.Add(minted...); err != nil {
		t.Fatalf("Add of %d tokens and keys: %v", len(minted), err)
	}
	if err := r.Add("a-literal-credential-1234"); err != nil {
		t.Fatalf("a literal value after them was refused: %v", err)
	}
	out := r.Redact("tok " + fakeJWT(7) + " key " + fakeKey(7) + " lit a-literal-credential-1234")
	for _, leaked := range []string{fakeJWT(7), fakeKey(7), "a-literal-credential-1234"} {
		if strings.Contains(out, leaked) {
			t.Errorf("%q survived redaction: %s", leaked, out)
		}
	}
}

// The registry of the run that hit the cap, 10039 lines, loads.
func TestForRun_registryOfMostlyTokensLoads(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	var b strings.Builder
	for i := 0; i < MaxValues; i++ {
		fmt.Fprintln(&b, fakeJWT(i))
	}
	for i := 0; i < 39; i++ {
		fmt.Fprintf(&b, "literal-secret-value-%04d\n", i)
	}
	if err := os.WriteFile(RegistryPath(state), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	red, err := ForRun(lookupFrom(nil), state)
	if err != nil {
		t.Fatalf("ForRun: %v", err)
	}
	if out := red.Redact("x literal-secret-value-0003 y"); strings.Contains(out, "literal-secret-value-0003") {
		t.Errorf("a registered literal was not masked: %s", out)
	}
}

// Only a value that is wholly a token or key goes to the token tier: one that
// merely contains one is an ordinary literal.
func TestWholeToken_wholeValuesOnly(t *testing.T) {
	for v, want := range map[string]bool{
		fakeJWT(1):                  true,
		fakeKey(1):                  true,
		"Bearer " + fakeJWT(1):      false,
		fakeKey(1) + "-suffix":      false,
		fakeRefresh(1, false):       true,
		fakeRefresh(1, true):        true,
		fakeRefresh(1, false)[1:]:   false,
		fakeRefresh(1, false) + "x": false,
		"plain-secret-value-12345":  false,
		"":                          false,
	} {
		if got := wholeToken(v); got != want {
			t.Errorf("wholeToken(%q) = %v, want %v", v, got, want)
		}
	}
}

// Tokens are persisted like every other value: the runner's redactor must
// hold them too.
func TestPersistTo_persistsTokensToo(t *testing.T) {
	path := filepath.Join(t.TempDir(), RegistryFileName)
	r := NewRedactor()
	r.PersistTo(path)
	if err := r.Add(fakeJWT(1), fakeKey(1), "literal-secret-value-0001"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("persisted %d values, want 3", len(got))
	}
}

// The shape patterns alone match across the boundary of tokens printed back to
// back, or of a token after a fragment that starts like one, and leave the
// second token's payload and signature in clear text. A registered token is
// masked whole by its literal whatever surrounds it.
func TestRedact_registeredTokensMaskedWholeWhenAdjacent(t *testing.T) {
	r := NewRedactor()
	if err := r.Add(fakeJWT(1), fakeJWT(2), fakeKey(1), fakeKey(2)); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]string{
		"jwt then jwt":      fakeJWT(1) + fakeJWT(2),
		"fragment then jwt": "eyJabcd.efgh." + fakeJWT(2),
		"key then key":      fakeKey(1) + fakeKey(2),
	} {
		out := r.Redact(in)
		for _, tok := range []string{fakeJWT(2), fakeKey(2)} {
			for _, seg := range strings.FieldsFunc(tok, func(c rune) bool { return c == '.' || c == '_' }) {
				if len(seg) >= 8 && strings.Contains(out, seg) {
					t.Errorf("%s: a piece of a registered token survived: %s", name, out)
				}
			}
		}
	}
}

// A shorter registered literal that happens to sit inside a token does not cut
// it: every value is replaced in one pass, the longest first at each position.
func TestRedact_literalInsideATokenDoesNotSplitIt(t *testing.T) {
	tok := fakeJWT(3)
	r := NewRedactor()
	if err := r.Add(tok, tok[4:16]); err != nil {
		t.Fatal(err)
	}
	if out := r.Redact("t=" + tok); out != "t="+Mask {
		t.Fatalf("Redact = %q, want the token masked whole", out)
	}
}

// The token tier has its own bound and fails closed past it.
func TestAdd_tokenTierFailsClosedPastItsCap(t *testing.T) {
	r := NewRedactor()
	minted := make([]string, 0, MaxTokenValues+1)
	for i := 0; i <= MaxTokenValues; i++ {
		minted = append(minted, fakeJWT(i))
	}
	if err := r.Add(minted...); !errors.Is(err, ErrTooManyValues) {
		t.Fatalf("Add of %d tokens = %v, want ErrTooManyValues", len(minted), err)
	}
	if len(r.tokens) != MaxTokenValues {
		t.Fatalf("holds %d tokens, want the cap %d", len(r.tokens), MaxTokenValues)
	}
	if err := r.Add(minted[MaxTokenValues]); !errors.Is(err, ErrTooManyValues) {
		t.Fatalf("offering the refused token again = %v, want ErrTooManyValues", err)
	}
}

// A literal refused at the cap is refused again when offered again: it was
// never held, so it must not pass as already known.
func TestAdd_valueRefusedAtTheCapIsRefusedAgain(t *testing.T) {
	r := NewRedactor()
	vals := make([]string, 0, MaxValues+1)
	for i := 0; i <= MaxValues; i++ {
		vals = append(vals, fmt.Sprintf("literal-secret-%08d", i))
	}
	if err := r.Add(vals...); !errors.Is(err, ErrTooManyValues) {
		t.Fatalf("Add past the cap = %v, want ErrTooManyValues", err)
	}
	if err := r.Add(vals[MaxValues]); !errors.Is(err, ErrTooManyValues) {
		t.Fatalf("offering the refused value again = %v, want ErrTooManyValues", err)
	}
}

// A registered value that contains a token ("key:secret") is masked whole,
// not cut at the token with the rest left in clear text.
func TestRedact_valueAroundATokenMaskedWhole(t *testing.T) {
	key := fakeKey(5)
	r := NewRedactor()
	if err := r.Add(key, key+":hunter2hunter2", "prefix-"+fakeJWT(5)); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{key + ":hunter2hunter2", "prefix-" + fakeJWT(5)} {
		if out := r.Redact("x " + in + " y"); out != "x "+Mask+" y" {
			t.Errorf("Redact(%q) = %q, want it masked whole", in, out)
		}
	}
}

// A value added after a Redact is masked by the next one.
func TestRedact_valueAddedAfterARedactIsMasked(t *testing.T) {
	r := NewRedactor("literal-secret-first")
	_ = r.Redact("warm up")
	if err := r.Add(fakeJWT(9), "literal-secret-second"); err != nil {
		t.Fatal(err)
	}
	out := r.Redact(fakeJWT(9) + " literal-secret-second literal-secret-first")
	if out != Mask+" "+Mask+" "+Mask {
		t.Fatalf("Redact = %q", out)
	}
}

// Parallel tests register what they mint while others redact.
func TestRedact_concurrentAddAndRedact(t *testing.T) {
	r := NewRedactor()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok := fakeJWT(1000 + i)
			if err := r.Add(tok); err != nil {
				t.Error(err)
				return
			}
			if out := r.Redact("t " + tok); out != "t "+Mask {
				t.Errorf("a token added by this goroutine was not masked: %q", out)
			}
		}(i)
	}
	wg.Wait()
}
