package report

import (
	"github.com/DeBrosOfficial/network/e2e/harness/artifacts"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// redactInput is the report boundary: every text that came from a test
// process, a node or a run step is redacted before anything is built from it,
// so report.json, the JUnit and HTML files, the summary and the bug drafts
// all carry the redacted text. A nil in.Redactor still masks every
// recognised shape of credential.
func redactInput(in Input) Input {
	red := in.Redactor
	out := in
	out.Results = redactResults(red, in.Results)
	out.Rerun = redactResults(red, in.Rerun)
	out.RunErrors = redactStrings(red, in.RunErrors)
	out.Evidence = make([]evidence.Record, len(in.Evidence))
	for i, rec := range in.Evidence {
		rec.Summary, rec.Error = red.Redact(rec.Summary), red.Redact(rec.Error)
		rec.Input, rec.Output = red.Redact(rec.Input), red.Redact(rec.Output)
		out.Evidence[i] = rec
	}
	out.PackageStderr = map[string]string{}
	for k, v := range in.PackageStderr {
		out.PackageStderr[k] = red.Redact(v)
	}
	if in.Artifacts != nil {
		ix := artifacts.Index{Files: make([]artifacts.File, len(in.Artifacts.Files))}
		for i, f := range in.Artifacts.Files {
			f.Command, f.Error = red.Redact(f.Command), red.Redact(f.Error)
			ix.Files[i] = f
		}
		out.Artifacts = &ix
	}
	return out
}

func redactResults(red *secrets.Redactor, rs []gotest.Result) []gotest.Result {
	if rs == nil {
		return nil
	}
	out := make([]gotest.Result, len(rs))
	for i, r := range rs {
		r.Output = red.Redact(r.Output)
		out[i] = r
	}
	return out
}

func redactStrings(red *secrets.Redactor, ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = red.Redact(s)
	}
	return out
}
