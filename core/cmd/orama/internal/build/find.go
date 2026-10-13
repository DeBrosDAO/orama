package build

import "fmt"

// ArchiveDir is where a build leaves its binary archive by default.
//
// Nothing searches it. Deploying commands take the archive by path (--archive)
// or use the one they just built: "the newest archive in /tmp" was another
// checkout's build often enough on a shared machine.
const ArchiveDir = "/tmp"

// archivePrefix, archiveInfix and archiveSuffix bracket the name a build
// produces: orama-<version>-linux-<arch>.tar.gz.
const (
	archivePrefix = "orama-"
	archiveInfix  = "-linux-"
	archiveSuffix = ".tar.gz"
)

// ArchiveName returns the file name a build of this version and architecture
// produces.
func ArchiveName(version, arch string) string {
	return fmt.Sprintf("%s%s%s%s%s", archivePrefix, version, archiveInfix, arch, archiveSuffix)
}
