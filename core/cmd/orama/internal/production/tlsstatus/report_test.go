package tlsstatus

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
)

var testNow = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func leaf(subject, issuer string, dns []string, notAfter time.Time) *tls.ConnectionState {
	return &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{
		Subject: pkix.Name{CommonName: subject}, Issuer: pkix.Name{CommonName: issuer}, DNSNames: dns,
		NotBefore: testNow.Add(-time.Hour), NotAfter: notAfter,
	}}}
}

// deps is a node whose Caddyfile is generatedCaddyfile, with the journal given
// and a handshake answered per SNI by serve.
func deps(journalText string, serve func(sni string) (*tls.ConnectionState, error)) (Deps, *[]string) {
	var asked []string
	return Deps{
		ReadFile: func(string) ([]byte, error) { return []byte(generatedCaddyfile), nil },
		Journal:  func(context.Context, string, int) (string, error) { return journalText, nil },
		Handshake: func(_ context.Context, addr, sni string) (*tls.ConnectionState, error) {
			asked = append(asked, addr+" "+sni)
			return serve(sni)
		},
		Now: func() time.Time { return testNow },
	}, &asked
}

func TestCollect_everyNameHeld(t *testing.T) {
	expires := testNow.Add(80 * 24 * time.Hour)
	d, asked := deps("", func(sni string) (*tls.ConnectionState, error) {
		return leaf("example.org", "(STAGING) Counterfeit Cashew R10", []string{"example.org", "*.example.org", "push.example.org"}, expires), nil
	})
	r, err := Collect(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if r.Unit != CaddyUnit || r.CA != "https://acme-v02.api.letsencrypt.org/directory" || len(r.Names) != 3 {
		t.Fatalf("%+v", r)
	}
	for _, n := range r.Names {
		if !n.Held || n.Issuer != "(STAGING) Counterfeit Cashew R10" || !n.NotAfter.Equal(expires) || n.LastError != nil || n.ServeError != "" {
			t.Errorf("%s: %+v", n.Name, n)
		}
	}
	// The wildcard is asked with a name under it, on the port the Caddyfile names.
	if want := "127.0.0.1:8443 tls-status.example.org"; (*asked)[0] != want {
		t.Errorf("asked %q, want %q first", (*asked)[0], want)
	}
}

// The stagenet failure: Caddy answers the handshake with "tls: internal error"
// because it holds nothing. A failure logged for a name this Caddyfile does not
// manage is not attributed to one it does.
func TestCollect_anErrorForAnotherNameIsNotAttributed(t *testing.T) {
	d, _ := deps(journal(infoLine, rateLimitedLine), func(sni string) (*tls.ConnectionState, error) {
		return nil, errors.New("remote error: tls: internal error")
	})
	r, err := Collect(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	var base Name
	for _, n := range r.Names {
		if n.Name == "stagenet.example.org" {
			t.Fatal("the journal's name is not one of this Caddyfile's")
		}
		if n.Name == "example.org" {
			base = n
		}
		if n.Held || n.ServeError != "remote error: tls: internal error" {
			t.Errorf("%s: %+v", n.Name, n)
		}
	}
	if base.LastError != nil {
		t.Errorf("an error for another name landed on example.org: %+v", base.LastError)
	}
}

func TestCollect_rateLimitedErrorLandsOnItsName(t *testing.T) {
	line := strings.Replace(rateLimitedLine, `"identifier":"stagenet.example.org"`, `"identifier":"example.org"`, 1)
	d, _ := deps(journal(line), func(string) (*tls.ConnectionState, error) { return &tls.ConnectionState{}, nil })
	r, err := Collect(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	var base, wild Name
	for _, n := range r.Names {
		switch n.Name {
		case "example.org":
			base = n
		case "*.example.org":
			wild = n
		}
	}
	if base.Held || base.LastError == nil || !base.LastError.RateLimited() || base.LastError.RetryAfter.IsZero() {
		t.Errorf("example.org: %+v", base)
	}
	if wild.LastError != nil {
		t.Errorf("the wildcard has no failure of its own: %+v", wild.LastError)
	}
}

func TestCollect_selfSignedExpiredAndForeignCertificatesAreNotHeld(t *testing.T) {
	cases := map[string]*tls.ConnectionState{
		"self-signed": leaf("example.org", "example.org", []string{"example.org", "*.example.org", "push.example.org"}, testNow.Add(time.Hour)),
		"expired":     leaf("example.org", "CA", []string{"example.org", "*.example.org", "push.example.org"}, testNow.Add(-time.Minute)),
		"other name":  leaf("other.net", "CA", []string{"other.net"}, testNow.Add(time.Hour)),
		"none":        {},
	}
	for name, state := range cases {
		d, _ := deps("", func(string) (*tls.ConnectionState, error) { return state, nil })
		r, err := Collect(context.Background(), d)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range r.Names {
			if n.Held {
				t.Errorf("%s: %s counted as held", name, n.Name)
			}
		}
	}
}

func TestCollect_failuresToReadAreErrorsThatSayWhatToCheck(t *testing.T) {
	d, _ := deps("", func(string) (*tls.ConnectionState, error) { return nil, nil })
	d.ReadFile = func(string) ([]byte, error) { return nil, errors.New("no such file") }
	if _, err := Collect(context.Background(), d); err == nil || !strings.Contains(err.Error(), CaddyfilePath) {
		t.Errorf("a missing Caddyfile: %v", err)
	}
	d, _ = deps("", func(string) (*tls.ConnectionState, error) { return nil, nil })
	d.Journal = func(context.Context, string, int) (string, error) { return "", errors.New("exit status 1") }
	if _, err := Collect(context.Background(), d); err == nil || !strings.Contains(err.Error(), CaddyUnit) {
		t.Errorf("an unreadable journal: %v", err)
	}
}

func TestCollect_aCaddyfileWithNoSitesReportsNoNames(t *testing.T) {
	d, _ := deps("", func(string) (*tls.ConnectionState, error) { return nil, nil })
	d.ReadFile = func(string) ([]byte, error) { return []byte("{\n}\n"), nil }
	r, err := Collect(context.Background(), d)
	if err != nil || r.Names == nil || len(r.Names) != 0 {
		t.Fatalf("%+v, %v", r, err)
	}
	if out, _ := json.Marshal(r); !strings.Contains(string(out), `"names":[]`) {
		t.Errorf("JSON %s: names must be a list, not null", out)
	}
}

func TestReport_jsonShape(t *testing.T) {
	at := time.Date(2026, 10, 11, 4, 26, 1, 0, time.UTC)
	retry := time.Date(2026, 10, 12, 4, 26, 1, 0, time.UTC)
	r := Report{Unit: CaddyUnit, CA: "https://ca/directory", Names: []Name{
		{Name: "example.org", Held: false, ServeError: "tls: internal error", LastError: &ObtainError{
			At: at, CA: "https://ca/directory", Status: 429, Type: "rateLimited", Message: "too many", RetryAfter: retry}},
	}}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"unit":"orama-namespace-caddy@index","ca":"https://ca/directory","names":[{"name":"example.org","held":false,"serve_error":"tls: internal error","last_error":{"at":"2026-10-11T04:26:01Z","ca":"https://ca/directory","status":429,"type":"rateLimited","message":"too many","retry_after":"2026-10-12T04:26:01Z"}}]}`
	if string(out) != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestRender_textNamesTheCAsReason(t *testing.T) {
	var out bytes.Buffer
	p := printer.New(&out, &out)
	retry := time.Date(2026, 10, 12, 4, 26, 1, 0, time.UTC)
	r := &Report{CA: "https://ca/directory", Names: []Name{
		{Name: "*.example.org", Held: true, Issuer: "R10", NotAfter: retry},
		{Name: "example.org", LastError: &ObtainError{Status: 429, Type: "rateLimited", Message: "too many certificates", RetryAfter: retry}},
		{Name: "push.example.org", ServeError: "connection refused"},
		{Name: "node.example.org"},
	}}
	if err := render(p, r); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"Certificate authority: https://ca/directory",
		"held", "issued by R10, expires 2026-10-12 04:26:01 UTC",
		"the CA refused: rate limited",
		"example.org: the last order failed: the CA answered HTTP 429 rateLimited: too many certificates",
		"The CA will accept a new order after 2026-10-12 04:26:01 UTC.",
		"connection refused",
		"no certificate served and no failed order in the journal",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}

func TestRender_noNames(t *testing.T) {
	var out bytes.Buffer
	if err := render(printer.New(&out, &out), &Report{}); err != nil || !strings.Contains(out.String(), "manages no certificate") {
		t.Errorf("%q, %v", out.String(), err)
	}
}
