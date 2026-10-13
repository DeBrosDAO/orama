package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	// DefaultRegressionPercent is how much slower (time/op) a benchmark may get against the
	// baseline before the gate fails: more than this is a regression.
	DefaultRegressionPercent = 25.0
	// MinSamples is the fewest runs per side benchstat needs before its significance verdict means
	// anything (the Mann-Whitney test cannot reach p<0.05 with fewer than 4 a side). With fewer the
	// gate refuses to judge instead of passing everything as noise.
	MinSamples = 5

	timeUnit   = "sec/op"
	geomeanRow = "geomean"
	noChange   = "~"

	// The columns of a benchstat csv row: name, baseline value and interval, new value and
	// interval, the change against the baseline, and the p-value with the sample counts.
	csvNameColumn = 0
	csvOldValue   = 1
	csvNewValue   = 3
	csvVsBase     = 5
	csvPValue     = 6
)

// ErrNoBenchmarks means the benchstat output held no time/op row to judge: an empty input is a
// broken pipeline, never a pass.
var ErrNoBenchmarks = errors.New("benchstat output has no sec/op benchmark rows")

// ErrTooFewSamples means a compared benchmark had fewer than MinSamples runs on a side.
var ErrTooFewSamples = errors.New("too few samples to judge a regression")

// Row is one benchmark's time/op comparison, as benchstat -format csv prints it.
type Row struct {
	Name string
	// Old and New are the median seconds per operation; zero when the side has no result.
	Old, New float64
	// Significant is benchstat's verdict that the difference is not noise.
	Significant bool
	// Samples is the smaller number of runs of the two sides.
	Samples int
}

// Delta is the change of time/op against the baseline in percent: positive is slower.
func (r Row) Delta() float64 { return (r.New/r.Old - 1) * 100 }

// Verdict is what the gate found.
type Verdict struct {
	Regressions []Row
	// Missing are baseline benchmarks with no result in the new run.
	Missing []string
	// New are benchmarks with no baseline.
	New []string
	// Compared counts the benchmarks present on both sides.
	Compared int
}

// OK reports whether the gate passes.
func (v Verdict) OK() bool { return len(v.Regressions) == 0 && len(v.Missing) == 0 }

// ParseBenchstatCSV reads `benchstat -format csv old new` and returns its sec/op rows. Tables of
// other units (B/op, allocs/op) and the geomean row are skipped.
func ParseBenchstatCSV(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	var rows []Row
	inTime := false
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read benchstat csv: %w", err)
		}
		if isUnitHeader(rec) {
			inTime = cell(rec, csvOldValue) == timeUnit
			continue
		}
		if !inTime || rec[csvNameColumn] == "" || rec[csvNameColumn] == geomeanRow {
			continue
		}
		row, err := parseRow(rec)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, ErrNoBenchmarks
	}
	return rows, nil
}

// isUnitHeader recognises the second line of a benchstat table: an empty first cell and the unit.
func isUnitHeader(rec []string) bool {
	return cell(rec, csvNameColumn) == "" && strings.Contains(cell(rec, csvOldValue), "/")
}

// cell is column i of a record, empty when benchstat left the row short (a benchmark with a result
// on one side only).
func cell(rec []string, i int) string {
	if i >= len(rec) {
		return ""
	}
	return rec[i]
}

func parseRow(rec []string) (Row, error) {
	row := Row{Name: rec[csvNameColumn]}
	var err error
	if row.Old, err = parseSeconds(cell(rec, csvOldValue)); err != nil {
		return Row{}, fmt.Errorf("%s: baseline value: %w", row.Name, err)
	}
	if row.New, err = parseSeconds(cell(rec, csvNewValue)); err != nil {
		return Row{}, fmt.Errorf("%s: new value: %w", row.Name, err)
	}
	vs := cell(rec, csvVsBase)
	row.Significant = vs != "" && vs != noChange
	row.Samples = minSamples(cell(rec, csvPValue))
	return row, nil
}

// parseSeconds reads a value; a side with no result is an empty cell and reads as zero.
func parseSeconds(value string) (float64, error) {
	if value == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number: %w", value, err)
	}
	return v, nil
}

// minSamples reads the "n=6" or "n=6+5" part of benchstat's P column ("p=0.002 n=6") and returns
// the smaller side; 0 when the column carries none.
func minSamples(pcolumn string) int {
	_, after, found := strings.Cut(pcolumn, "n=")
	fields := strings.Fields(after)
	if !found || len(fields) == 0 {
		return 0
	}
	least := 0
	for _, n := range strings.Split(fields[0], "+") {
		v, err := strconv.Atoi(n)
		if err != nil {
			return 0
		}
		if least == 0 || v < least {
			least = v
		}
	}
	return least
}

// Judge applies the gate: a benchmark fails when its time/op is more than thresholdPercent slower
// than the baseline and benchstat says the difference is not noise; a baseline benchmark with no
// new result fails too, because a benchmark that stopped running cannot regress visibly.
func Judge(rows []Row, thresholdPercent float64) (Verdict, error) {
	var v Verdict
	for _, r := range rows {
		switch {
		case r.Old == 0 && r.New == 0:
			continue
		case r.New == 0:
			v.Missing = append(v.Missing, r.Name)
		case r.Old == 0:
			v.New = append(v.New, r.Name)
		default:
			if r.Samples < MinSamples {
				return Verdict{}, fmt.Errorf("%w: %s has %d run(s) a side, need %d: run with a higher -count", ErrTooFewSamples, r.Name, r.Samples, MinSamples)
			}
			v.Compared++
			if r.Significant && r.Delta() > thresholdPercent {
				v.Regressions = append(v.Regressions, r)
			}
		}
	}
	sort.Slice(v.Regressions, func(i, j int) bool { return v.Regressions[i].Delta() > v.Regressions[j].Delta() })
	sort.Strings(v.Missing)
	sort.Strings(v.New)
	return v, nil
}
