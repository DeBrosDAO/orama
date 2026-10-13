package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResultLine_collapsesWhitespaceIntoOneLine(t *testing.T) {
	require.Equal(t, "PASS blocks - height 9 ok", pass("blocks", "height  %d\nok", 9).Line())
	require.Equal(t, "FAIL x", fail("x", "").Line())
	require.Equal(t, "SKIP archive - cause", skip("archive", "cause").Line())
}

func TestReport_countsFailuresAndPrintsASummary(t *testing.T) {
	var out bytes.Buffer
	n := Report(&out, []Result{pass("a", "ok"), fail("b", "bad"), skip("c", "env"), fail("d", "worse")})
	require.Equal(t, 2, n)
	require.Contains(t, out.String(), "SUMMARY 1 pass, 2 fail, 1 skip\n")
	require.Contains(t, out.String(), "FAIL b - bad\n")
}

func TestReport_noResultsIsNoFailure(t *testing.T) {
	var out bytes.Buffer
	require.Zero(t, Report(&out, nil))
	require.Contains(t, out.String(), "SUMMARY 0 pass, 0 fail, 0 skip")
}
