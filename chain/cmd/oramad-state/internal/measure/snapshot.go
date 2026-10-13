package measure

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-state/internal/stategen"
)

// snapshotLine is what `oramad snapshots export` prints.
var snapshotLine = regexp.MustCompile(`height (\d+), format (\d+), chunks (\d+)`)

// SnapshotResult is the cost of taking a state-sync snapshot of a home and of restoring it into a
// fresh one. The restore is the application half of a state sync (the chunks applied to empty
// stores); the transfer of chunks between peers is not in it.
type SnapshotResult struct {
	Home   string `json:"home"`
	Height uint64 `json:"height"`
	Format uint32 `json:"format"`
	Chunks uint32 `json:"chunks"`

	CreateSeconds float64 `json:"create_seconds"`
	// SnapshotBytes is the size of the snapshot store after the export: the chunk files.
	SnapshotBytes int64   `json:"snapshot_store_bytes"`
	DumpSeconds   float64 `json:"dump_archive_seconds"`
	// ArchiveBytes is the portable archive, which holds the chunks compressed.
	ArchiveBytes   int64   `json:"archive_bytes"`
	LoadSeconds    float64 `json:"load_seconds"`
	RestoreSeconds float64 `json:"restore_seconds"`

	// RestoredApplicationDBBytes and RestoredNullifierDBBytes are the fresh home's databases after
	// the restore.
	RestoredApplicationDBBytes int64 `json:"restored_application_db_bytes"`
	RestoredNullifierDBBytes   int64 `json:"restored_nullifier_db_bytes"`
}

// Snapshot exports a snapshot of the home's latest state, dumps it to an archive in workDir, loads
// the archive into a fresh home made beside it and restores it there. The source home must not be
// in use. The fresh home is left in workDir/restored for the caller to remove.
func Snapshot(ctx context.Context, o Oramad, home, workDir string) (SnapshotResult, error) {
	res := SnapshotResult{Home: home}
	export, err := o.Run(ctx, "snapshots", "export", "--home", home)
	if err != nil {
		return res, err
	}
	res.CreateSeconds = export.Elapsed.Seconds()
	if err := parseSnapshotLine(export.Combined(), &res); err != nil {
		return res, err
	}
	if res.SnapshotBytes, err = stategen.DirSize(filepath.Join(home, "data", "snapshots")); err != nil {
		return res, err
	}
	archive := filepath.Join(workDir, "snapshot.tar.gz")
	height, format := strconv.FormatUint(res.Height, 10), strconv.FormatUint(uint64(res.Format), 10)
	dump, err := o.Run(ctx, "snapshots", "dump", height, format, "--output", archive, "--home", home)
	if err != nil {
		return res, err
	}
	res.DumpSeconds = dump.Elapsed.Seconds()
	if res.ArchiveBytes, err = fileBytes(archive); err != nil {
		return res, err
	}
	return restoreFresh(ctx, o, home, filepath.Join(workDir, "restored"), archive, res)
}

func parseSnapshotLine(out string, res *SnapshotResult) error {
	m := snapshotLine.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("the snapshot export printed no height, format and chunk count: %s", tail(out))
	}
	height, _ := strconv.ParseUint(m[1], 10, 64)
	format, _ := strconv.ParseUint(m[2], 10, 32)
	chunks, _ := strconv.ParseUint(m[3], 10, 32)
	res.Height, res.Format, res.Chunks = height, uint32(format), uint32(chunks)
	return nil
}

// restoreFresh makes an empty home with the source's genesis, loads the archive into its snapshot
// store and restores it.
func restoreFresh(ctx context.Context, o Oramad, source, fresh, archive string, res SnapshotResult) (SnapshotResult, error) {
	if _, err := o.Run(ctx, "init", moniker, "--chain-id", DefaultChainID, "--default-denom", denom, "--home", fresh); err != nil {
		return res, err
	}
	genesis, err := os.ReadFile(filepath.Join(source, "config", "genesis.json"))
	if err != nil {
		return res, fmt.Errorf("failed to read the source genesis: %w", err)
	}
	if err := os.WriteFile(filepath.Join(fresh, "config", "genesis.json"), genesis, 0o600); err != nil {
		return res, fmt.Errorf("failed to write the fresh home's genesis: %w", err)
	}
	load, err := o.Run(ctx, "snapshots", "load", archive, "--home", fresh)
	if err != nil {
		return res, err
	}
	res.LoadSeconds = load.Elapsed.Seconds()
	height, format := strconv.FormatUint(res.Height, 10), strconv.FormatUint(uint64(res.Format), 10)
	restore, err := o.Run(ctx, "snapshots", "restore", height, format, "--home", fresh)
	if err != nil {
		return res, err
	}
	res.RestoreSeconds = restore.Elapsed.Seconds()
	sizes, err := stategen.MeasureSizes(fresh)
	res.RestoredApplicationDBBytes, res.RestoredNullifierDBBytes = sizes.ApplicationDB, sizes.NullifierDB
	return res, err
}
