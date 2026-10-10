package releasepub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// uploadAssets creates the GitHub release for the tag if there is none and
// uploads the archives it does not hold yet. An archive already there is
// skipped when it has the size (and, where GitHub reports one, the digest) of
// the one the cut listed, so a publish that stopped after the upload can be run
// again; one that differs is an error, because a published archive is never
// overwritten.
func (p PublishParams) uploadAssets(ctx context.Context, pending *Pending) error {
	fmt.Fprintf(p.Progress, "Uploading %d archive(s) to %s release %s...\n", len(pending.Assets), p.GitHubRepo, pending.Tag)
	create := []string{"release", "create", pending.Tag, "--repo", p.GitHubRepo,
		"--title", pending.Channel + " " + pending.Version,
		"--notes", fmt.Sprintf("Orama %s %s. Install it through the TUF metadata at the release host; this page is only where the bytes live.", pending.Channel, pending.Version)}
	if pending.Channel != ChannelMain {
		create = append(create, "--prerelease")
	}
	view := []string{"release", "view", pending.Tag, "--repo", p.GitHubRepo, "--json", "assets"}
	if p.DryRun {
		p.print("gh", view, "(create it if it is not there, and upload the archives it lacks:)")
		p.print("gh", create, "")
		p.print("gh", uploadArgs(pending, pending.Assets, p.GitHubRepo), "")
		return nil
	}
	held, err := p.heldAssets(ctx, view, create)
	if err != nil {
		return err
	}
	var missing []Asset
	for _, a := range pending.Assets {
		have, ok := held[a.Name]
		if !ok {
			missing = append(missing, a)
			continue
		}
		if err := sameAsset(a, have); err != nil {
			return err
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if _, err := p.Run(ctx, "gh", uploadArgs(pending, missing, p.GitHubRepo)...); err != nil {
		return fmt.Errorf("upload the archives to %s: %w", pending.Tag, err)
	}
	return nil
}

func uploadArgs(pending *Pending, assets []Asset, repo string) []string {
	args := []string{"release", "upload", pending.Tag, "--repo", repo}
	for _, a := range assets {
		args = append(args, a.Path)
	}
	return args
}

// githubAsset is one asset of a release as `gh release view --json assets` lists it.
type githubAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

// heldAssets are the release's assets by name, after creating the release if
// there is none. Any other failure of gh is an error.
func (p PublishParams) heldAssets(ctx context.Context, view, create []string) (map[string]githubAsset, error) {
	out, err := p.Run(ctx, "gh", view...)
	switch {
	case err == nil:
	case strings.Contains(string(out), releaseNotFound):
		if _, err := p.Run(ctx, "gh", create...); err != nil {
			return nil, fmt.Errorf("create the GitHub release: %w", err)
		}
		return map[string]githubAsset{}, nil
	default:
		return nil, fmt.Errorf("look up the GitHub release (is gh installed and logged in?): %w", err)
	}
	var doc struct {
		Assets []githubAsset `json:"assets"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("read the GitHub release's assets: %w", err)
	}
	held := make(map[string]githubAsset, len(doc.Assets))
	for _, a := range doc.Assets {
		held[a.Name] = a
	}
	return held, nil
}

// sameAsset refuses a published asset that is not the archive the cut listed.
func sameAsset(want Asset, have githubAsset) error {
	info, err := os.Stat(want.Path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", want.Path, err)
	}
	if have.Size != info.Size() {
		return fmt.Errorf("the GitHub release already holds %s with %d bytes, not the cut's %d: a published archive is never overwritten (cut a new version)", want.Name, have.Size, info.Size())
	}
	if have.Digest != "" && have.Digest != "sha256:"+want.SHA256 {
		return fmt.Errorf("the GitHub release already holds %s with digest %s, not the cut's sha256:%s: a published archive is never overwritten (cut a new version)", want.Name, have.Digest, want.SHA256)
	}
	return nil
}
