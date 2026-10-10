package setup

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// releaseIPs are n machines, 203.0.113.101 and up.
func releaseIPs(n int) []string {
	ips := make([]string, n)
	for i := range ips {
		ips[i] = fmt.Sprintf("203.0.113.%d", 101+i)
	}
	return ips
}

// clusterOnly is a run that stops at the cluster node: the release step is what
// these tests are about.
func clusterOnly(h *harness, ips ...string) Options {
	opts := h.opts(ips...)
	opts.ClusterOnly, opts.StorageGB, opts.Name = true, 0, ""
	return opts
}

func TestRelease_everyMachineDownloadsItAtOnceAndTheLaptopAcceptsTheMatchingHashes(t *testing.T) {
	h := newHarness()
	ips := releaseIPs(5)
	h.enroll.gate = newFetchGate(len(ips))
	mustRun(t, h, clusterOnly(h, ips...))

	if got := h.enroll.gate.maxSeen(); got != len(ips) {
		t.Errorf("%d machines were downloading at once, want all %d: they are not fetched in parallel", got, len(ips))
	}
	for _, ip := range ips {
		if h.w.index("download "+ip+" "+testReleaseURL) < 0 {
			t.Errorf("%s did not download the archive from the repository:\n%s", ip, strings.Join(h.w.entries(), "\n"))
		}
		if h.w.index("stage "+ip) < 0 || !h.report.has(ip, StepRelease, StateDone) {
			t.Errorf("%s did not stage the release", ip)
		}
	}
	if h.w.count("endorse ") != 1 {
		t.Errorf("the operator's wallet signs the release once, not once per machine; got %d", h.w.count("endorse "))
	}
	if h.w.index("fetch release") >= 0 {
		t.Error("this computer downloaded the archive although the machines do")
	}
	last := -1
	for _, ip := range ips {
		last = max(last, h.w.index("stage "+ip))
	}
	if h.w.count("accept release") != 1 || h.w.index("accept release") < last {
		t.Errorf("the rollback record is raised once, after every machine confirmed the archive:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.count("remove release metadata") != 1 {
		t.Error("the metadata fetched here is removed")
	}
}

func TestRelease_atMostMaxReleaseFetchesMachinesDownloadAtOnce(t *testing.T) {
	h := newHarness()
	ips := releaseIPs(maxReleaseFetches + 3)
	h.enroll.gate = newFetchGate(maxReleaseFetches)
	mustRun(t, h, clusterOnly(h, ips...))
	if got := h.enroll.gate.maxSeen(); got != maxReleaseFetches {
		t.Errorf("%d machines downloaded at once, want the limit %d", got, maxReleaseFetches)
	}
	for _, ip := range ips {
		if h.w.index("stage "+ip) < 0 {
			t.Errorf("%s was not given the release", ip)
		}
	}
}

func TestRelease_aMachineThatReportsAnotherArchiveIsRefused(t *testing.T) {
	h := newHarness()
	h.enroll.releases = map[string]fakeRelease{ip2: {downloadSHA: strings.Repeat("0", 64)}}
	_, err := run(t, h, clusterOnly(h, ip1, ip2, ip3))
	if err == nil || !strings.Contains(err.Error(), ip2) || !strings.Contains(err.Error(), strings.Repeat("0", 64)) || !strings.Contains(err.Error(), testArchiveSHA) {
		t.Fatalf("got %v, want the machine and both digests named", err)
	}
	if h.w.index("stage "+ip2) >= 0 {
		t.Error("an archive other than the signed one was staged")
	}
	if h.w.index("discard "+ip2) < 0 {
		t.Error("the refused download stays on the machine")
	}
	if h.w.index("accept release") >= 0 || h.w.index("cluster ") >= 0 {
		t.Errorf("the run went on after a machine reported another archive:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if !h.report.has(ip2, StepRelease, StateFailed) {
		t.Error("the refusal is not reported on the machine")
	}
}

func TestRelease_aMachineThatCannotReachTheRepositoryFailsWithTheURLAndIsNotGivenAnUpload(t *testing.T) {
	h := newHarness()
	h.enroll.releases = map[string]fakeRelease{ip2: {fetchErr: errRepositoryUnreachable}}
	_, err := run(t, h, clusterOnly(h, ip1, ip2))
	if err == nil {
		t.Fatal("a machine that could not download the release was passed")
	}
	for _, want := range []string{ip2, testReleaseURL, "Could not resolve host", "--upload-release"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
	if h.w.index("fetch release") >= 0 || h.w.index("stage "+ip2) >= 0 {
		t.Errorf("setup uploaded the archive by itself:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("cluster ") >= 0 || h.w.index("accept release") >= 0 {
		t.Error("the run went on after a machine failed to download")
	}
}

func TestRelease_aMachineThatAlreadyRunsTheReleaseIsSkippedAndItsDownloadRemoved(t *testing.T) {
	h := newHarness()
	f := installedFacts()
	f.GlobalInstalled, f.ChainActive = false, false
	h.enroll.facts = map[string]Facts{ip1: f}
	mustRun(t, h, h.opts(ip1, ip2))
	if h.w.index("stage "+ip1) >= 0 {
		t.Errorf("a machine that runs exactly this build was given it again:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("stage "+ip2) < 0 {
		t.Error("the fresh machine was not given the release")
	}
	if !h.report.has(ip1, StepRelease, StateSkipped) {
		t.Error("the skip is not reported")
	}
	if h.w.index("discard "+ip1) < 0 {
		t.Error("the download of a machine that did not need it stays on it")
	}
	if h.w.index("accept release") < 0 {
		t.Error("the release was checked by the machines, and the rollback record was not raised")
	}
}

func TestRelease_aMachineThatEndsUpWithAnotherBuildIsRefused(t *testing.T) {
	h := newHarness()
	other := strings.Repeat("9", 64)
	h.enroll.releases = map[string]fakeRelease{ip1: {stagedManifest: other}}
	_, err := run(t, h, clusterOnly(h, ip1))
	if err == nil || !strings.Contains(err.Error(), ip1) || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), testManifest) {
		t.Fatalf("got %v, want the machine and both manifests named", err)
	}
	if h.w.index("accept release") >= 0 || h.w.index("cluster ") >= 0 {
		t.Error("the run went on with a machine that runs another build than the endorsed one")
	}
}

func TestRelease_machinesThatDownloadedTheSameFileMustReportTheSameManifest(t *testing.T) {
	h := newHarness()
	h.enroll.releases = map[string]fakeRelease{ip2: {manifest: []byte(`{"version":"0.3.1","arch":"amd64","commit":"other"}`)}}
	_, err := run(t, h, clusterOnly(h, ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), "different manifest") {
		t.Fatalf("got %v", err)
	}
	if h.w.count("endorse ") != 1 {
		t.Errorf("the wallet signed %d times; a second signature is never given for a disagreeing machine", h.w.count("endorse "))
	}
}

func TestRelease_aRefusedSignatureStagesNothingAndRemovesTheDownloads(t *testing.T) {
	h := newHarness()
	h.deps.Releases = fakeReleases{w: h.w, endorseErr: errors.New("the RootWallet agent is locked")}
	// Both machines are downloading before either hears of the refusal.
	h.enroll.gate = newFetchGate(2)
	_, err := run(t, h, clusterOnly(h, ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("stage ") >= 0 || h.w.index("accept release") >= 0 {
		t.Errorf("a release the operator did not sign was staged:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.count("discard ") != 2 {
		t.Errorf("both downloads are removed, got %d", h.w.count("discard "))
	}
}

func TestRelease_aStageThatFailsOnTheMachineIsReportedAndItsDownloadRemoved(t *testing.T) {
	h := newHarness()
	h.enroll.releases = map[string]fakeRelease{ip1: {stageErr: errors.New("manifest.sig does not recover to a trusted signer")}}
	_, err := run(t, h, clusterOnly(h, ip1))
	if err == nil || !strings.Contains(err.Error(), ip1) || !strings.Contains(err.Error(), "trusted signer") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("discard "+ip1) < 0 {
		t.Error("the download stays on the machine")
	}
}

func TestRelease_aReleaseThatCannotBeResolvedTouchesNoMachine(t *testing.T) {
	h := newHarness()
	h.deps.Releases = fakeReleases{w: h.w, resolveErr: errors.New("the nightly channel lists no release for linux/amd64")}
	_, err := run(t, h, clusterOnly(h, ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), "no release for linux/amd64") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("download ") >= 0 || h.w.index("stage ") >= 0 {
		t.Error("a machine was told to download a release that was not verified")
	}
}

func TestRelease_uploadReleaseKeepsTheLaptopDownloadingAndUploading(t *testing.T) {
	h := newHarness()
	opts := clusterOnly(h, ip1, ip2)
	opts.UploadRelease = true
	mustRun(t, h, opts)
	if h.w.count("fetch release") != 1 || h.w.index("stage "+ip1) < 0 || h.w.index("stage "+ip2) < 0 {
		t.Errorf("the archive is downloaded here once and staged on each machine:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("resolve release") >= 0 || h.w.index("download ") >= 0 {
		t.Error("a machine downloaded the release although --upload-release was given")
	}
}

func TestRelease_machinesOfTwoArchitecturesGetTheirOwnRelease(t *testing.T) {
	h := newHarness()
	arm := freshFacts()
	arm.Arch = "arm64"
	h.enroll.facts = map[string]Facts{ip2: arm}
	mustRun(t, h, clusterOnly(h, ip1, ip2))
	if h.w.index("resolve release amd64") < 0 || h.w.index("resolve release arm64") < 0 {
		t.Errorf("each architecture is resolved:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.count("accept release") != 2 {
		t.Errorf("each architecture's release is accepted, got %d", h.w.count("accept release"))
	}
}

func TestCommandLine_carriesUploadRelease(t *testing.T) {
	o := Options{Network: "stagenet", IPs: []string{ip1}, Name: "alice", UploadRelease: true, StorageGB: DefaultStorageGB}
	if line := o.CommandLine(); !strings.Contains(line, " --upload-release") {
		t.Errorf("a run that was told to upload would download on the next try: %s", line)
	}
	o.UploadRelease = false
	if line := o.CommandLine(); strings.Contains(line, "upload-release") {
		t.Errorf("a run that downloads names --upload-release: %s", line)
	}
}
