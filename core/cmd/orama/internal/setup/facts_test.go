package setup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const factsOutput = "cpu=4\nram_kb=8388608\ndisk_kb=104857600\narch=x86_64\ncluster=1\nglobal=0\nmanifest_sha=" + testManifest + "\n"

func TestParseFacts(t *testing.T) {
	f, err := ParseFacts(factsOutput)
	if err != nil {
		t.Fatal(err)
	}
	if f.Arch != "amd64" || !f.ClusterInstalled || f.GlobalInstalled || f.ManifestSHA256 != testManifest {
		t.Fatalf("%+v", f)
	}
	if f.Hardware.CPUCores != 4 || f.Hardware.RAMBytes != 8<<30 {
		t.Fatalf("hardware %+v", f.Hardware)
	}
}

func TestParseFacts_arm64(t *testing.T) {
	f, err := ParseFacts(strings.Replace(factsOutput, "x86_64", "aarch64", 1))
	if err != nil || f.Arch != "arm64" {
		t.Fatalf("%+v, %v", f, err)
	}
}

func TestParseFacts_refusals(t *testing.T) {
	for name, out := range map[string]string{
		"unknown architecture": strings.Replace(factsOutput, "x86_64", "riscv64", 1),
		"no hardware":          "arch=x86_64\ncluster=0\nglobal=0\n",
		"a digest that is not": strings.Replace(factsOutput, testManifest, "nothex", 1),
	} {
		if _, err := ParseFacts(out); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestProbeScript_readsTheUnitsAndFiles(t *testing.T) {
	for _, want := range []string{"nproc", "/proc/meminfo", "orama-node.service", "orama-global-chain.service", "/opt/orama/manifest.json"} {
		if !strings.Contains(probeScript, want) {
			t.Errorf("the probe does not look at %s", want)
		}
	}
}

func TestPollUntil_doneOnTheFirstCheck(t *testing.T) {
	calls := 0
	err := pollUntil(context.Background(), time.Hour, time.Hour, "x", func(context.Context) (bool, error) { calls++; return true, nil })
	if err != nil || calls != 1 {
		t.Fatalf("%d calls, %v: the first check is immediate", calls, err)
	}
}

func TestPollUntil_failureEndsAtOnce(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	err := pollUntil(context.Background(), time.Millisecond, time.Second, "x", func(context.Context) (bool, error) { calls++; return false, boom })
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("%d calls, %v", calls, err)
	}
}

func TestPollUntil_deadline(t *testing.T) {
	err := pollUntil(context.Background(), time.Millisecond, 15*time.Millisecond, "the thing", func(context.Context) (bool, error) { return false, nil })
	if err == nil || !strings.Contains(err.Error(), "gave up waiting for the thing") {
		t.Fatalf("got %v", err)
	}
}

func TestPollUntil_cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := pollUntil(ctx, time.Millisecond, time.Hour, "x", func(context.Context) (bool, error) { return false, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestPollUntil_becomesTrueLater(t *testing.T) {
	calls := 0
	err := pollUntil(context.Background(), time.Millisecond, time.Second, "x", func(context.Context) (bool, error) { calls++; return calls == 3, nil })
	if err != nil || calls != 3 {
		t.Fatalf("%d calls, %v", calls, err)
	}
}
