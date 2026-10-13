package planinfo

import (
	"errors"
	"strings"
	"testing"
)

func digest(c string) string { return strings.Repeat(c, 64) }

func TestParse(t *testing.T) {
	oramad, verifier := strings.Repeat("ab", 32), strings.Repeat("cd", 32)
	info := func(binaries, verifiers string) string {
		return `{"binaries":{` + binaries + `},"verifiers":{` + verifiers + `}}`
	}
	entry := func(platform, sum string) string {
		return `"` + platform + `":"https://x.example/oramad?checksum=sha256:` + sum + `"`
	}
	tests := []struct {
		name    string
		info    string
		want    Digests
		wantErr string
	}{
		{"both of the platform", info(entry("linux/amd64", oramad), entry("linux/amd64", verifier)), Digests{oramad, verifier}, ""},
		{"an uppercase digest is not one the chain accepts", info(entry("linux/amd64", strings.ToUpper(oramad)), entry("linux/amd64", verifier)), Digests{}, "no oramad checksum"},
		{"a checksum before another parameter", info(`"linux/amd64":"https://x.example/o?checksum=sha256:`+oramad+`&archive=false"`, entry("linux/amd64", verifier)), Digests{oramad, verifier}, ""},
		{"another platform beside it", info(entry("linux/arm64", digest("0"))+","+entry("linux/amd64", oramad), entry("linux/amd64", verifier)), Digests{oramad, verifier}, ""},
		{"any is not this platform", info(entry("any", oramad), entry("linux/amd64", verifier)), Digests{}, "no oramad checksum for linux/amd64"},
		{"another platform only", info(entry("linux/arm64", oramad), entry("linux/amd64", verifier)), Digests{}, "no oramad checksum"},
		{"no verifier", info(entry("linux/amd64", oramad), ""), Digests{}, "no orama-orchard-verifier checksum"},
		{"no checksum in the url", info(`"linux/amd64":"https://x.example/oramad"`, entry("linux/amd64", verifier)), Digests{}, "no oramad checksum"},
		{"a short digest", info(entry("linux/amd64", oramad[:60]), entry("linux/amd64", verifier)), Digests{}, "no oramad checksum"},
		{"a longer digest is not a prefix match", info(entry("linux/amd64", oramad+"ab"), entry("linux/amd64", verifier)), Digests{}, "no oramad checksum"},
		{"free text", `upgrade to 0.4.0`, Digests{}, "carries no upgrade-info JSON"},
		{"empty", ``, Digests{}, "carries no upgrade-info JSON"},
		{"an unknown member", `{"note":"x",` + info(entry("linux/amd64", oramad), entry("linux/amd64", verifier))[1:], Digests{}, "unknown field"},
		{"data after the object", info(entry("linux/amd64", oramad), entry("linux/amd64", verifier)) + `{}`, Digests{}, "data after the JSON object"},
		{"only binaries", `{"binaries":{` + entry("linux/amd64", oramad) + `}}`, Digests{}, "no orama-orchard-verifier checksum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse("v2", tt.info, "linux/amd64")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse = %+v, %v; want error %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Parse = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestDigests_Check(t *testing.T) {
	want := Digests{Oramad: digest("a"), Verifier: digest("b")}
	tests := []struct {
		name     string
		digests  Digests
		oramad   string
		verifier string
		wantErr  string
	}{
		{"both match", want, digest("a"), digest("b"), ""},
		{"a different oramad", want, digest("c"), digest("b"), "names an oramad with sha256 aaaaaaaaaaaa, but this oramad is cccccccccccc"},
		{"a different verifier", want, digest("a"), digest("c"), "names an orama-orchard-verifier with sha256 bbbbbbbbbbbb, but this verifier is cccccccccccc"},
		{"the digests swapped", want, digest("b"), digest("a"), "oramad"},
		{"no digests at all", Digests{}, digest("a"), digest("b"), "names no usable checksum"},
		{"no verifier digest", Digests{Oramad: digest("a")}, digest("a"), digest("b"), "names no usable checksum"},
		{"an uppercase digest", Digests{Oramad: strings.ToUpper(digest("a")), Verifier: digest("b")}, digest("a"), digest("b"), "names no usable checksum"},
		{"a short digest", Digests{Oramad: "abc", Verifier: digest("b")}, "abc", digest("b"), "names no usable checksum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.digests.Check("v2", tt.oramad, tt.verifier)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Check = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr) || !errors.Is(err, ErrMismatch)):
				t.Fatalf("Check = %v, want %q wrapping ErrMismatch", err, tt.wantErr)
			}
		})
	}
}

func TestDigests_CheckOneFile(t *testing.T) {
	d := Digests{Oramad: digest("a"), Verifier: digest("b")}
	if err := d.CheckOramad("v2", digest("a")); err != nil {
		t.Errorf("the plan's oramad: %v", err)
	}
	if err := d.CheckVerifier("v2", digest("b")); err != nil {
		t.Errorf("the plan's verifier: %v", err)
	}
	if err := d.CheckOramad("v2", digest("b")); !errors.Is(err, ErrMismatch) {
		t.Errorf("the verifier's digest as the oramad: %v", err)
	}
	if err := d.CheckVerifier("v2", digest("a")); !errors.Is(err, ErrMismatch) {
		t.Errorf("the oramad's digest as the verifier: %v", err)
	}
	if err := (Digests{}).CheckOramad("v2", digest("a")); !errors.Is(err, ErrMismatch) {
		t.Errorf("a plan naming nothing: %v", err)
	}
}

func TestShort(t *testing.T) {
	if got := Short(digest("c")); got != strings.Repeat("c", 12) {
		t.Errorf("Short = %q", got)
	}
	if got := Short("abc"); got != "abc" {
		t.Errorf("Short of a short digest = %q", got)
	}
}
