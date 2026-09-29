//go:build e2e_fleet

package docsclaims

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

var (
	// cliMention is an inline-code `orama ...` in a doc.
	cliMention = regexp.MustCompile("`(orama(?: [^`]*)?)`")
	// commandWord is a word that can be a command name.
	commandWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// denial marks a line that says a command does not exist, which is a
	// correct thing for a doc to say about a missing command.
	denial = regexp.MustCompile(`(?i)there is no|no such|does not exist|doesn't exist|was removed|was renamed|used to be|no longer`)
)

// mention is one `orama ...` a doc names, and the command it resolves to.
type mention struct {
	Where line
	Text  string
	// Unknown is the first word that is neither a subcommand of the longest
	// documented prefix nor an argument of a leaf command; "" when the
	// mention resolves.
	Unknown string
}

// resolve finds the longest documented command prefix of words and reports
// the next word when that prefix is a group (so the word would have to be one
// of its subcommands).
func resolve(ref *cliconf.Reference, words []string) (prefix, unknown string) {
	path := []string{"orama"}
	for _, w := range words[1:] {
		if !commandWord.MatchString(w) {
			break
		}
		path = append(path, w)
	}
	k := len(path)
	for k > 1 {
		if _, ok := ref.Get(strings.Join(path[:k], " ")); ok {
			break
		}
		k--
	}
	prefix = strings.Join(path[:k], " ")
	c, ok := ref.Get(prefix)
	isGroup := k == 1 || (ok && c.Group())
	if k < len(path) && isGroup {
		return prefix, path[k]
	}
	return prefix, ""
}

// unknownMentions lists the command names docs use that the reference does
// not have, skipping lines that deny a command exists.
func unknownMentions(t testing.TB, ref *cliconf.Reference, files []string) []mention {
	t.Helper()
	var out []mention
	for _, f := range files {
		if f == cliRefDoc {
			continue
		}
		for _, l := range lines(t, f) {
			if denial.MatchString(l.Text) {
				continue
			}
			for _, m := range cliMention.FindAllStringSubmatch(l.Text, -1) {
				if _, unknown := resolve(ref, strings.Fields(m[1])); unknown != "" {
					out = append(out, mention{Where: l, Text: m[1], Unknown: unknown})
				}
			}
		}
	}
	return out
}

// confirmUnknown keeps the mentions the binary under test also refuses: a
// hidden command (serve-ipfs-cluster) is real though the reference omits it.
func confirmUnknown(t *testing.T, ms []mention) []mention {
	t.Helper()
	cli := harness.CLI(t)
	verdict := map[string]bool{}
	var out []mention
	for _, m := range ms {
		prefix := strings.Fields(m.Text)
		key := strings.Join(prefix[:indexOf(prefix, m.Unknown)+1], " ")
		refused, done := verdict[key]
		if !done {
			args := append(strings.Fields(key)[1:], "--help")
			refused = infra.Run(t, cli, args...).Exit != infra.ExitOK
			verdict[key] = refused
		}
		if refused {
			out = append(out, m)
		}
	}
	return out
}

func indexOf(words []string, w string) int {
	for i, x := range words {
		if x == w {
			return i
		}
	}
	return len(words) - 1
}

func report(t *testing.T, ms []mention) {
	t.Helper()
	sort.Slice(ms, func(i, j int) bool { return ms[i].Where.String() < ms[j].Where.String() })
	for _, m := range ms {
		t.Errorf("%s\n  names `%s`, but `%s` is not a command the binary has (docs/CLI_REFERENCE.md)", m.Where, m.Text, m.Unknown)
	}
}

// TestAPISurface_cliCommandNamesExist: every CLI command API_SURFACE.md says
// calls a route is a real command (bugboard 2855: `orama app deploy` and
// `orama namespace webrtc enable|disable|status` do not exist; the real ones
// are `orama deploy`, `orama namespace enable webrtc` and `webrtc-status`).
func TestAPISurface_cliCommandNamesExist(t *testing.T) {
	t.Parallel()
	report(t, confirmUnknown(t, unknownMentions(t, cliconf.LoadReference(t), []string{surfaceDoc})))
}

// TestDocs_cliCommandNamesExist: the same check over every document under
// docs/, so a renamed or removed command cannot live on in prose.
func TestDocs_cliCommandNamesExist(t *testing.T) {
	t.Parallel()
	var files []string
	for _, f := range docs(t) {
		if f != surfaceDoc {
			files = append(files, f)
		}
	}
	report(t, confirmUnknown(t, unknownMentions(t, cliconf.LoadReference(t), files)))
}
