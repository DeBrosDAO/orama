package report

import (
	"encoding/xml"
	"fmt"

	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
)

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Skipped   *junitMessage `xml:"skipped,omitempty"`
}

type junitMessage struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

// JUnit renders the report as JUnit XML: one suite per feature, one case per
// test. A feature that never ran is one skipped case, so CI shows the gap.
func JUnit(r Report) ([]byte, error) {
	root := junitSuites{Name: "orama-e2e-" + r.RunID}
	output := map[string]string{}
	for _, f := range r.Failures {
		output[f.Feature+" "+f.Test] = f.Output
	}
	for _, f := range r.Features {
		s := junitSuite{Name: f.ID}
		if f.Status == StatusMissing {
			s.Cases = append(s.Cases, junitCase{Name: "(package)", Classname: f.ID, Time: "0.000",
				Skipped: &junitMessage{Message: "the feature package produced no result"}})
		}
		if f.PackageOutput != "" {
			s.Cases = append(s.Cases, junitCase{Name: "(package)", Classname: f.ID, Time: "0.000",
				Failure: &junitMessage{Message: "package failed", Body: f.PackageOutput}})
		}
		for _, t := range f.Tests {
			c := junitCase{Name: t.Name, Classname: f.ID, Time: fmt.Sprintf("%.3f", t.Elapsed)}
			switch t.Status {
			case gotest.ActionFail:
				c.Failure = &junitMessage{Message: "failed (" + t.Flakiness + ")", Body: output[f.ID+" "+t.Name]}
			case gotest.ActionSkip:
				c.Skipped = &junitMessage{Message: "not covered: " + t.Reason}
			}
			s.Cases = append(s.Cases, c)
		}
		for _, c := range s.Cases {
			s.Tests++
			if c.Failure != nil {
				s.Failures++
			}
			if c.Skipped != nil {
				s.Skipped++
			}
		}
		root.Tests, root.Failures, root.Skipped = root.Tests+s.Tests, root.Failures+s.Failures, root.Skipped+s.Skipped
		root.Suites = append(root.Suites, s)
	}
	out, err := xml.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode JUnit report: %w", err)
	}
	return append([]byte(xml.Header), append(out, '\n')...), nil
}
