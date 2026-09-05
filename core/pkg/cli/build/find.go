package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ArchiveDir is where a build leaves its binary archive, and where every
// command that consumes one looks for it.
const ArchiveDir = "/tmp"

// archivePrefix and archiveSuffix bracket the name a build produces:
// orama-<version>-linux-<arch>.tar.gz.
const (
	archivePrefix = "orama-"
	archiveInfix  = "-linux-"
	archiveSuffix = ".tar.gz"
)

// ArchiveName returns the file name a build of this version and architecture
// produces. Callers that look for an archive and the code that writes one now
// agree by construction.
func ArchiveName(version, arch string) string {
	return fmt.Sprintf("%s%s%s%s%s", archivePrefix, version, archiveInfix, arch, archiveSuffix)
}

// FindNewestArchive returns the path of the most recently built archive, or ""
// when there is none.
//
// Three copies of this existed, in push, setup and sandbox, and they matched
// differently: two walked the directory and compared modification times, the
// third globbed and sorted, and a Stat that failed made the glob version's
// comparator return false for every pair — leaving the order whatever the
// filesystem happened to give. Which archive got deployed depended on which
// command was used to deploy it.
func FindNewestArchive() string {
	entries, err := os.ReadDir(ArchiveDir)
	if err != nil {
		return ""
	}

	var newest string
	var newestMod int64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, archivePrefix) ||
			!strings.Contains(name, archiveInfix) ||
			!strings.HasSuffix(name, archiveSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			// The file was removed between the listing and the stat. It cannot
			// be pushed either way.
			continue
		}
		if mod := info.ModTime().UnixNano(); mod > newestMod {
			newest = filepath.Join(ArchiveDir, name)
			newestMod = mod
		}
	}
	return newest
}
