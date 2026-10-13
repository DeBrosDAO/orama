package setup

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// maxReleaseFetches is the most machines that download the release at once. The
	// repository serves static files and each machine has its own link to it, so the
	// limit protects this computer's SSH sessions, not the repository.
	maxReleaseFetches = 8
	// releaseStageBudget bounds a machine's staging: the endorsed archive packed
	// again, and the stage command's verification and swap.
	releaseStageBudget = 30 * time.Minute
)

// uploadReleaseHint is what a machine that cannot reach the release repository is
// told.
const uploadReleaseHint = "if the machine cannot reach the release repository (a closed outbound firewall, a private network), " +
	"run setup with --upload-release to upload the archive from this computer instead"

// curlExit is how curl reports a failure in its message ("curl: (6) ...").
var curlExit = regexp.MustCompile(`curl: \((\d+)\)`)

// unreachableCurlExits are the curl exit codes of a repository the machine cannot
// reach: the proxy or host does not resolve (5, 6), the connection is refused or
// times out (7, 28), the TLS handshake or certificate fails (35, 51, 58, 59, 60,
// 77), or the transfer is cut (52, 55, 56). A repository that answered 404 (22), a
// download of the wrong size or a digest that differs are not these.
var unreachableCurlExits = map[int]bool{5: true, 6: true, 7: true, 28: true, 35: true, 51: true, 52: true, 55: true, 56: true, 58: true, 59: true, 60: true, 77: true}

// unreachable says whether err is a machine that could not reach the repository.
func unreachable(err error) bool {
	m := curlExit.FindStringSubmatch(err.Error())
	if m == nil {
		return false
	}
	code, convErr := strconv.Atoi(m[1])
	return convErr == nil && unreachableCurlExits[code]
}

// stageArch puts the release for one architecture on its machines: each
// downloads it itself, or, with --upload-release, this computer downloads it and
// uploads it to each in turn.
func (r *runner) stageArch(ctx context.Context, arch string, nodes []*nodeRun) error {
	if r.opts.UploadRelease {
		return r.uploadArch(ctx, arch, nodes)
	}
	return r.fetchArch(ctx, arch, nodes)
}

// releaseRun is one architecture's release while its machines fetch it.
type releaseRun struct {
	ref      *ReleaseRef
	endorser *releaseEndorser

	mu     sync.Mutex
	failed string
}

// markFailed remembers the first machine that failed, which the machines that are
// stopped because of it name.
func (rr *releaseRun) markFailed(ip string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rr.failed == "" {
		rr.failed = ip
	}
}

func (rr *releaseRun) firstFailure() string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.failed
}

// fetchArch verifies the channel's metadata here, a few kilobytes, and has every
// machine download the archive from the repository, at most maxReleaseFetches at
// once, instead of this computer downloading it once and pushing it to each
// machine over SSH one after the other.
//
// What is checked, and where. This computer verifies the metadata against the
// release root the network pins; that metadata gives the archive's length and
// SHA-256 and the SHA-256 of its manifest.json. Each machine checks the file it
// downloaded against the length and SHA-256, which travel over the SSH session
// and not through the repository. The digest a machine then reports is that same
// machine's word, and is compared here only as a second look. What this computer
// does check by itself is the manifest: before the operator's wallet is asked to
// sign anything, the manifest a machine reports must hash to the manifest digest
// in the signed metadata, for every machine, so a machine cannot get a signature
// on a manifest of its own making. The machine's own orama then verifies the
// archive against that signature before anything is put in place, and this
// computer reads the staged build back. Only when every machine has confirmed
// the archive is the rollback record raised.
func (r *runner) fetchArch(ctx context.Context, arch string, nodes []*nodeRun) error {
	r.d.Report.Linef("verifying the %s release metadata for linux/%s against the network's release root; each of the %d machines downloads the archive from %s itself",
		r.net.Manifest.Channel, arch, len(nodes), r.net.Manifest.ReleaseRepo)
	ref, err := r.d.Releases.Resolve(ctx, r.net, arch)
	if err != nil {
		return fmt.Errorf("release of the %s channel for linux/%s: %w", r.net.Manifest.Channel, arch, err)
	}
	defer func() {
		if rmErr := ref.Remove(); rmErr != nil {
			r.d.Report.Linef("could not remove the release metadata: %v", rmErr)
		}
	}()
	rr := &releaseRun{ref: ref, endorser: &releaseEndorser{src: r.d.Releases, ref: ref}}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxReleaseFetches)
	for _, n := range nodes {
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			return r.fetchOn(gctx, rr, n)
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	return ref.Accept()
}

// fetchOn is one machine's release: it downloads and checks the archive, its
// manifest is held to the signed metadata, the operator endorses it, and the
// machine stages it, unless it already runs that build. What it staged is read
// back and compared with what was endorsed.
//
// A stage that has begun is not cut short when another machine fails, and its
// directory is not removed from here: the stage command removes it itself, and
// taking it away while `orama node stage-archive` is swapping /opt/orama would
// leave that half done.
func (r *runner) fetchOn(ctx context.Context, rr *releaseRun, n *nodeRun) error {
	ip, ref := n.plan.IP, rr.ref
	r.emit(ip, StepRelease, StateRunning, "downloading "+ref.Version+" from the release repository")
	f, err := n.m.FetchRelease(ctx, ref)
	if err != nil {
		return r.releaseFailed(rr, ip, fmt.Errorf("download release %s from %s: %w", ref.Version, ref.URL, err), unreachable(err), ctx.Err() != nil)
	}
	stageBegun := false
	defer func() {
		if !stageBegun {
			r.discardFetched(ctx, n, f)
		}
	}()
	if f.SHA256 != ref.SHA256 {
		return r.releaseFailed(rr, ip, fmt.Errorf("the machine reports the archive it downloaded from %s has sha256 %s, and the signed metadata says %s", ref.URL, f.SHA256, ref.SHA256), false, false)
	}
	if err := checkManifestDigest(ref, f.Manifest); err != nil {
		return r.releaseFailed(rr, ip, err, false, false)
	}
	end, err := rr.endorser.endorse(ctx, f.Manifest)
	if err != nil {
		return r.releaseFailed(rr, ip, err, false, ctx.Err() != nil)
	}
	if n.facts.ManifestSHA256 == end.ManifestSHA256 && n.facts.CLISHA256 == end.CLISHA256 {
		r.skip(ip, StepRelease, "already runs "+ref.Version)
		return nil
	}
	if err := ctx.Err(); err != nil {
		return r.releaseFailed(rr, ip, err, false, true)
	}
	stageBegun = true
	r.emit(ip, StepRelease, StateRunning, "staging "+ref.Version)
	if err := r.stageRelease(ctx, n, f, end); err != nil {
		return r.releaseFailed(rr, ip, err, false, false)
	}
	r.emit(ip, StepRelease, StateDone, ref.Version)
	return nil
}

// stageRelease stages the fetched release and reads the machine's build back, with
// a context of its own: it ends when it is done or at the budget, not when a
// sibling machine's failure cancels the run.
func (r *runner) stageRelease(ctx context.Context, n *nodeRun, f *FetchedRelease, end *Endorsement) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseStageBudget)
	defer cancel()
	if err := n.m.StageFetched(ctx, f, end); err != nil {
		return fmt.Errorf("stage the release: %w", err)
	}
	return r.confirmStaged(ctx, n, end)
}

// releaseFailed reports the failure of a machine and returns its error. A machine
// that was stopped because another failed first says so and returns the context's
// error, which the run does not report: the machine that failed first is the
// error. hint adds the way out for a repository the machine cannot reach.
func (r *runner) releaseFailed(rr *releaseRun, ip string, err error, hint, stopped bool) error {
	if first := rr.firstFailure(); stopped && first != "" && first != ip {
		r.emit(ip, StepRelease, StateFailed, "cancelled: "+first+" failed")
		return context.Canceled
	}
	rr.markFailed(ip)
	r.emit(ip, StepRelease, StateFailed, err.Error())
	if hint {
		err = fmt.Errorf("%w; %s", err, uploadReleaseHint)
	}
	return fmt.Errorf("machine %s: %w", ip, err)
}

// confirmStaged reads the machine's build back and refuses it unless it is the
// one the operator endorsed: a machine does not get to say which release it
// ended up with.
func (r *runner) confirmStaged(ctx context.Context, n *nodeRun, end *Endorsement) error {
	facts, err := n.m.Probe(ctx)
	if err != nil {
		return fmt.Errorf("read back the release the machine staged: %w", err)
	}
	if facts.ManifestSHA256 != end.ManifestSHA256 || facts.CLISHA256 != end.CLISHA256 {
		return fmt.Errorf("the machine runs manifest %s with CLI %s after staging, not the endorsed manifest %s with CLI %s",
			facts.ManifestSHA256, facts.CLISHA256, end.ManifestSHA256, end.CLISHA256)
	}
	return nil
}

// discardFetched removes a download that was not staged. The run's context may
// be the reason it was not, so the removal does not use it.
func (r *runner) discardFetched(ctx context.Context, n *nodeRun, f *FetchedRelease) {
	if err := n.m.DiscardFetched(context.WithoutCancel(ctx), f); err != nil {
		r.d.Report.Linef("could not remove the download on %s: %v", n.plan.IP, err)
	}
}

// releaseEndorser has the operator sign the manifest of an archive once, however
// many machines downloaded it. Every machine's manifest has been held to the one
// digest the signed metadata names before it gets here, so they are all the same
// bytes, and the answer is the same for all: a signature, or the refusal, which
// is remembered so that the next machine does not put the same prompt to the
// operator again.
type releaseEndorser struct {
	src ReleaseSource
	ref *ReleaseRef

	mu   sync.Mutex
	done bool
	end  *Endorsement
	err  error
}

func (e *releaseEndorser) endorse(ctx context.Context, manifest []byte) (*Endorsement, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done {
		return e.end, e.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.end, e.err = e.src.Endorse(ctx, e.ref, manifest)
	e.done = true
	return e.end, e.err
}
