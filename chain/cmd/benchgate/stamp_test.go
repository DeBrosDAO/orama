package main

import (
	"errors"
	"strings"
	"testing"
)

var (
	stableBox = Host{OS: "linux", Arch: "amd64", CPUs: 8}
	laptop    = Host{OS: "darwin", Arch: "arm64", CPUs: 8}
)

func TestNewStamp_aStableHostWritesAReleaseBaseline(t *testing.T) {
	s, err := NewStamp(stableBox, true, false)
	if err != nil || !s.Release {
		t.Fatalf("stamp = %+v, err = %v", s, err)
	}
	if bigger, err := NewStamp(Host{OS: "linux", Arch: "amd64", CPUs: 64}, true, false); err != nil || !bigger.Release {
		t.Fatalf("a 64-core box: %+v, %v", bigger, err)
	}
}

func TestNewStamp_refusesABaselineOffAStableHost(t *testing.T) {
	for name, h := range map[string]Host{
		"laptop":        laptop,
		"linux arm":     {OS: "linux", Arch: "arm64", CPUs: 16},
		"small linux":   {OS: "linux", Arch: "amd64", CPUs: 4},
		"seven cores":   {OS: "linux", Arch: "amd64", CPUs: MinStableCPUs - 1},
		"zero cpus":     {OS: "linux", Arch: "amd64"},
		"empty machine": {},
	} {
		if _, err := NewStamp(h, true, false); !errors.Is(err, ErrUnstableHost) {
			t.Errorf("%s: err = %v, want ErrUnstableHost", name, err)
		}
	}
}

func TestNewStamp_overrideWritesAFileMarkedNotRelease(t *testing.T) {
	s, err := NewStamp(laptop, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if s.Release || !strings.Contains(s.Line(), "release=false") || !strings.Contains(s.Line(), "stable=false") {
		t.Fatalf("stamp line %q must say stable=false release=false", s.Line())
	}
}

func TestNewStamp_aPlainRunIsNeverARelease(t *testing.T) {
	s, err := NewStamp(stableBox, false, false)
	if err != nil || s.Release {
		t.Fatalf("stamp = %+v, err = %v", s, err)
	}
	if !strings.Contains(s.Line(), "stable=true") {
		t.Fatalf("a run on a stable box says so: %q", s.Line())
	}
	if _, err := NewStamp(laptop, false, false); err != nil {
		t.Fatalf("a plain run on a laptop must work: %v", err)
	}
}

func TestParseStamp_roundTripsAndFindsItAmongBenchmarkOutput(t *testing.T) {
	want, _ := NewStamp(stableBox, true, false)
	file := want.Line() + "\ngoos: linux\nBenchmarkX-8 \t 10\t 100 ns/op\nPASS\n"
	got, err := ParseStamp(strings.NewReader(file))
	if err != nil || got != want {
		t.Fatalf("got %+v, %v, want %+v", got, err, want)
	}
}

func TestParseStamp_missingAndMalformed(t *testing.T) {
	if _, err := ParseStamp(strings.NewReader("goos: linux\nBenchmarkX-8 10 100 ns/op\n")); !errors.Is(err, ErrNoStamp) {
		t.Errorf("no stamp: err = %v", err)
	}
	if _, err := ParseStamp(strings.NewReader("")); !errors.Is(err, ErrNoStamp) {
		t.Errorf("empty file: err = %v", err)
	}
	if _, err := ParseStamp(strings.NewReader(stampPrefix + " cpus=many\n")); err == nil {
		t.Error("a stamp with cpus=many was accepted")
	}
}

func TestCheckProvenance(t *testing.T) {
	release, _ := NewStamp(stableBox, true, false)
	run, _ := NewStamp(stableBox, false, false)
	laptopRun, _ := NewStamp(laptop, false, false)
	notRelease, _ := NewStamp(laptop, true, true)

	if err := CheckProvenance(release, run, false); err != nil {
		t.Errorf("release baseline vs a stable run: %v", err)
	}
	if err := CheckProvenance(notRelease, notRelease, false); err == nil {
		t.Error("a baseline marked not-release gated a release")
	}
	if err := CheckProvenance(release, release, false); err != nil {
		t.Errorf("comparing the baseline with itself: %v", err)
	}
	if err := CheckProvenance(release, laptopRun, false); err == nil {
		t.Error("a laptop run was compared with a linux/amd64 baseline")
	}
	unstableLinux, _ := NewStamp(Host{OS: "linux", Arch: "amd64", CPUs: 2}, false, false)
	if err := CheckProvenance(release, unstableLinux, false); err == nil {
		t.Error("a 2-CPU run gated a release")
	}
	if err := CheckProvenance(release, unstableLinux, true); err != nil {
		t.Errorf("the override must allow an unstable run on the same platform: %v", err)
	}
	if err := CheckProvenance(release, laptopRun, true); err == nil {
		t.Error("the override must not allow comparing different platforms")
	}
}
