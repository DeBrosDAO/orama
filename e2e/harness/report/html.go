package report

import (
	"bytes"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/coverage"
)

var funcs = template.FuncMap{
	"fmtTime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.UTC().Format("2006-01-02 15:04:05Z")
	},
	"secs":  func(f float64) string { return fmt.Sprintf("%.1fs", f) },
	"kinds": kinds,
	"count": func(c map[string]int, k string) int { return c[k] },
	"sortedKeys": func(m map[string][]string) []string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	},
	"join": strings.Join,
}

func kinds(res *coverage.Result) []string {
	counts := res.Counts()
	out := make([]string, 0, len(counts))
	for k := range counts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var page = template.Must(template.New("report").Funcs(funcs).Parse(pageTemplate))

// HTML renders the self-contained report page: no scripts, fonts or
// stylesheets are fetched, so it opens from an archived artifact dir.
func HTML(r Report) ([]byte, error) {
	var b bytes.Buffer
	if err := page.Execute(&b, r); err != nil {
		return nil, fmt.Errorf("failed to render the HTML report: %w", err)
	}
	return b.Bytes(), nil
}

const pageTemplate = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Orama e2e {{.RunID}}</title>
<style>
:root{--bg:#fff;--fg:#1b1f24;--muted:#5b6470;--line:#d8dde3;--pass:#1a7f37;--fail:#cf222e;--warn:#9a6700;--card:#f6f8fa}
@media (prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#8d96a0;--line:#30363d;--pass:#3fb950;--fail:#f85149;--warn:#d29922;--card:#161b22}}
body{background:var(--bg);color:var(--fg);font:14px/1.5 system-ui,sans-serif;margin:0 auto;max-width:1100px;padding:16px}
h1,h2{margin:24px 0 8px}table{border-collapse:collapse;width:100%;margin:8px 0}td,th{border-bottom:1px solid var(--line);padding:4px 8px;text-align:left;vertical-align:top}
pre{background:var(--card);padding:8px;overflow-x:auto;white-space:pre-wrap;word-break:break-word;font-size:12px}
.banner{padding:16px;border-radius:8px;font-size:20px;font-weight:600;color:#fff}
.PASS{background:var(--pass)}.FAIL{background:var(--fail)}.INCOMPLETE{background:var(--warn)}
.pass{color:var(--pass)}.fail{color:var(--fail)}.skip,.missing,.uncovered{color:var(--warn)}.muted{color:var(--muted)}
details{margin:4px 0}summary{cursor:pointer}
</style></head><body>
<div class="banner {{.Verdict}}">{{.Verdict}} — run {{.RunID}} @ {{.Commit}}</div>
<p>{{.Notification}}</p>
{{if .VerdictReasons}}<ul>{{range .VerdictReasons}}<li>{{.}}</li>{{end}}</ul>{{end}}
<table><tr><th>Features</th><th>Tests</th><th>Passed</th><th>Failed</th><th>Flaky</th><th>Not covered</th></tr>
<tr><td>{{.Totals.Features}}</td><td>{{.Totals.Tests}}</td><td class="pass">{{.Totals.Passed}}</td><td class="fail">{{.Totals.Failed}}</td><td>{{.Totals.Flaky}}</td><td class="skip">{{.Totals.NotCovered}}</td></tr></table>
{{if .RunErrors}}<h2>Run errors</h2><ul>{{range .RunErrors}}<li class="fail">{{.}}</li>{{end}}</ul>{{end}}

<h2>Stage timeline</h2>
<table><tr><th>#</th><th>Stage</th><th>Start</th><th>End</th><th>Duration</th><th>Features</th><th></th></tr>
{{range .Timeline}}<tr><td>{{.ID}}</td><td>{{.Name}}</td><td>{{fmtTime .Start}}</td><td>{{fmtTime .End}}</td><td>{{.Seconds}}s</td><td>{{join .Features ", "}}</td><td>{{if .Failed}}<span class="fail">failed</span>{{else}}<span class="pass">ok</span>{{end}}</td></tr>
{{else}}<tr><td colspan="7" class="muted">No stage ran.</td></tr>{{end}}</table>

<h2>Bugs found</h2>
{{range .BugsByArea}}<h3>{{.Area}}</h3>
{{range .Failures}}<details><summary><span class="fail">{{.Feature}} / {{.Test}}</span> <span class="muted">({{.Flakiness}}{{if .Subtasks}}; tasks {{range .Subtasks}}#{{.}} {{end}}{{end}})</span></summary>
<pre>{{.Output}}</pre>
{{range .Evidence}}<details><summary>{{.Kind}} {{.Summary}} → {{.Status}} ({{.DurationMS}}ms){{if .Error}} <span class="fail">{{.Error}}</span>{{end}}</summary>
{{if .Input}}<pre>{{.Input}}</pre>{{end}}{{if .Output}}<pre>{{.Output}}</pre>{{end}}</details>{{end}}
</details>{{end}}
{{else}}<p class="pass">No failures.</p>{{end}}

<h2>Features</h2>
<table><tr><th>Stage</th><th>Feature</th><th>Area</th><th>Status</th><th>Tests</th></tr>
{{range .Features}}<tr><td>{{.Stage}}{{if .Destructive}} <span class="muted">destructive</span>{{end}}</td><td>{{.ID}}<br><span class="muted">{{.Title}}</span></td><td>{{.Area}}</td><td class="{{.Status}}">{{.Status}}</td>
<td>{{if .PackageOutput}}<details><summary class="fail">package output</summary><pre>{{.PackageOutput}}</pre></details>{{end}}{{range .Tests}}<div class="{{.Status}}">{{.Status}} {{.Name}} <span class="muted">{{secs .Elapsed}}{{if .Reason}} — {{.Reason}}{{end}}{{if .Flakiness}} — {{.Flakiness}}{{end}}</span></div>{{end}}</td></tr>
{{end}}</table>

<h2>What worked</h2>
{{if .Worked}}<ul>{{range .Worked}}<li class="pass">{{.}}</li>{{end}}</ul>{{else}}<p class="muted">Nothing passed completely.</p>{{end}}

<h2>Coverage</h2>
{{with .Coverage}}{{$c := .Counts}}
<table><tr><th>Kind</th><th>Covered</th><th>Waived</th><th>Uncovered</th></tr>
{{range kinds .}}{{$k := index $c .}}<tr><td>{{.}}</td><td>{{count $k "covered"}}</td><td>{{count $k "waived"}}</td><td class="uncovered">{{count $k "uncovered"}}</td></tr>{{end}}</table>
{{if .Uncovered}}<details open><summary class="uncovered">Uncovered ({{len .Uncovered}})</summary><pre>{{join .Uncovered "\n"}}</pre></details>{{end}}
{{if .UnknownCovers}}<details open><summary class="fail">Unknown covers</summary><pre>{{join .UnknownCovers "\n"}}</pre></details>{{end}}
{{if .StaleWaivers}}<details open><summary class="fail">Stale waivers</summary><pre>{{join .StaleWaivers "\n"}}</pre></details>{{end}}
{{if .InvalidWaivers}}<details open><summary class="fail">Invalid waivers</summary><pre>{{join .InvalidWaivers "\n"}}</pre></details>{{end}}
{{if .Claims}}<h3>Doc claims under test</h3><ul>{{$cl := .Claims}}{{range sortedKeys .Claims}}<li>{{.}} <span class="muted">({{join (index $cl .) ", "}})</span></li>{{end}}</ul>{{end}}
<details><summary>Matrix ({{len .Rows}} items)</summary><table><tr><th>Item</th><th>Status</th><th>By</th></tr>
{{range .Rows}}<tr><td>{{.ID}}</td><td class="{{.Status}}">{{.Status}}</td><td>{{if .Features}}{{join .Features ", "}}{{else if .Waiver}}{{.Waiver.Reason}} <span class="muted">until {{.Waiver.Trigger}}</span>{{end}}</td></tr>{{end}}
</table></details>
{{else}}<p class="skip">The coverage gate was not evaluated.</p>{{end}}

{{if .FailedArtifact}}<h2>Artifacts that could not be collected</h2><ul>{{range .FailedArtifact}}<li>{{.Source}}: <code>{{.Command}}</code> — {{.Error}} (<a href="collected/{{.Path}}">{{.Path}}</a>)</li>{{end}}</ul>{{end}}
<p class="muted">Collected artifacts: <a href="collected/index.json">collected/index.json</a> · evidence: <code>evidence/</code> · raw results: <code>gotest/</code></p>
</body></html>
`
