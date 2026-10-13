package measure

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ExportResult is the cost of `oramad export` on a home: the genesis document of the whole state.
type ExportResult struct {
	Home string `json:"home"`
	// Seconds is the wall time of the command, process start and state load included.
	Seconds float64 `json:"export_seconds"`
	// Bytes is the size of the genesis document written.
	Bytes int64 `json:"genesis_bytes"`
}

// Export runs `oramad export` on the home, writing the document into workDir, and removes the
// document afterwards unless keep is set. The home must not be in use.
func Export(ctx context.Context, o Oramad, home, workDir string, keep bool) (ExportResult, error) {
	res := ExportResult{Home: home}
	doc := filepath.Join(workDir, "exported-genesis.json")
	out, err := o.Run(ctx, "export", "--home", home, "--output-document", doc)
	if err != nil {
		return res, err
	}
	res.Seconds = out.Elapsed.Seconds()
	if res.Bytes, err = fileBytes(doc); err != nil {
		return res, err
	}
	if keep {
		return res, nil
	}
	if err := os.Remove(doc); err != nil {
		return res, fmt.Errorf("failed to remove %s: %w", doc, err)
	}
	return res, nil
}

// fileBytes is the size of a file.
func fileBytes(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("failed to measure %s: %w", path, err)
	}
	return info.Size(), nil
}
