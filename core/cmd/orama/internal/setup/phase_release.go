package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"
)

// maxReleaseFetches is the most machines that download the release at once. The
// repository serves static files and each machine has its own link to it, so the
// limit protects this computer's SSH sessions, not the repository.
const maxReleaseFetches = 8

// uploadReleaseHint is what a machine that cannot download the release is told.
const uploadReleaseHint = "if the machine cannot reach the release repository (a closed outbound firewall, a private network), " +
	"run setup with --upload-release to upload the archive from this computer instead"

// stageArch puts the release for one architecture on its machines: each
// downloads it itself, or, with --upload-release, this computer downloads it and
// uploads it to each in turn.
func (r *runner) stageArch(ctx context.Context, arch string, nodes []*nodeRun) error {
	if r.opts.UploadRelease {
		return r.uploadArch(ctx, arch, nodes)
	}
	return r.fetchArch(ctx, arch, nodes)
}

// fetchArch verifies the channel's metadata here, a few kilobytes, and has every
// machine download the archive from the repository, at most maxReleaseFetches at
// once, instead of this computer downloading it once and pushing it to each
// machine over SSH one after the other.
//
// The trust is the same as an upload's. The metadata is verified here against
// the release root the network pins. Each machine checks the archive it
// downloaded against the length and SHA-256 that verified metadata gives (the
// numbers travel over the SSH session, not through the repository), and reports
// the digest; it is compared here again. The operator signs the manifest from
// that archive, and the machine's own orama verifies the archive against that
// signature before anything is put in place. Only when every machine has
// confirmed the archive is the rollback record raised.
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
	endorser := &releaseEndorser{src: r.d.Releases, ref: ref}
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
			return r.fetchOn(gctx, ref, endorser, n)
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	return ref.Accept()
}

// fetchOn is one machine's release: it downloads and checks the archive, the
// operator endorses it, and the machine stages it, unless it already runs that
// build. What it staged is read back and compared with what was endorsed.
func (r *runner) fetchOn(ctx context.Context, ref *ReleaseRef, endorser *releaseEndorser, n *nodeRun) error {
	ip := n.plan.IP
	fail := func(err error) error {
		r.emit(ip, StepRelease, StateFailed, err.Error())
		return fmt.Errorf("machine %s: %w", ip, err)
	}
	r.emit(ip, StepRelease, StateRunning, "downloading "+ref.Version+" from the release repository")
	f, err := n.m.FetchRelease(ctx, ref)
	if err != nil {
		r.emit(ip, StepRelease, StateFailed, fmt.Sprintf("download %s: %v", ref.URL, err))
		return fmt.Errorf("machine %s: download release %s from %s: %w; %s", ip, ref.Version, ref.URL, err, uploadReleaseHint)
	}
	staged := false
	defer func() {
		if !staged {
			r.discardFetched(ctx, n, f)
		}
	}()
	if f.SHA256 != ref.SHA256 {
		return fail(fmt.Errorf("the machine reports the archive it downloaded from %s has sha256 %s, and the signed metadata says %s", ref.URL, f.SHA256, ref.SHA256))
	}
	end, err := endorser.endorse(ctx, f.Manifest)
	if err != nil {
		return fail(err)
	}
	if n.facts.ManifestSHA256 == end.ManifestSHA256 && n.facts.CLISHA256 == end.CLISHA256 {
		r.skip(ip, StepRelease, "already runs "+ref.Version)
		return nil
	}
	r.emit(ip, StepRelease, StateRunning, "staging "+ref.Version)
	if err := n.m.StageFetched(ctx, f, end); err != nil {
		return fail(fmt.Errorf("stage release %s: %w", ref.Version, err))
	}
	staged = true
	if err := r.confirmStaged(ctx, n, end); err != nil {
		return fail(err)
	}
	r.emit(ip, StepRelease, StateDone, ref.Version)
	return nil
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
// many machines downloaded it: they all hold the same file, so they all report
// the same manifest, and one that reports another is refused the signature.
type releaseEndorser struct {
	src ReleaseSource
	ref *ReleaseRef

	mu       sync.Mutex
	manifest []byte
	done     *Endorsement
}

func (e *releaseEndorser) endorse(ctx context.Context, manifest []byte) (*Endorsement, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.done != nil {
		if !bytes.Equal(manifest, e.manifest) {
			return nil, errors.New("the machine reports a different manifest inside the archive than the machine the release was endorsed from, " +
				"though both downloaded the same file; the operator's signature is not given for it")
		}
		return e.done, nil
	}
	end, err := e.src.Endorse(ctx, e.ref, manifest)
	if err != nil {
		return nil, err
	}
	e.manifest, e.done = manifest, end
	return end, nil
}
