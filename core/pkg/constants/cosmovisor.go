package constants

// Cosmovisor is the process manager the global chain unit runs oramad under.
// The pin is the official cosmos/cosmos-sdk release cosmovisor/v1.7.3, tag
// object 6dee2e6fe23d94e1f13cee8890f7e8fc490edf98. The digests are the
// SHA-256 of the release's linux tarballs, taken from the release's own
// published SHA256SUMS-cosmovisor-v1.7.3.txt and equal to the digest the
// GitHub release API reports for each asset. `orama global install` accepts
// only a staged tarball with exactly this digest.
const (
	CosmovisorVersion = "v1.7.3"
	// CosmovisorBinary is the file name inside the tarball and in GlobalBinDir.
	CosmovisorBinary = "cosmovisor"
)

// CosmovisorTarballSHA256 is the pinned digest of the release tarball, by GOARCH.
var CosmovisorTarballSHA256 = map[string]string{
	"amd64": "3df6ef38cf976b00d226f391dc6866b8dc4040fc2f1b4a780d248f6e1cc9332e",
	"arm64": "ff27992e1356fbcb858a604455ad28a9727415c3e35b947a4fdb30d8f91295cd",
}

// CosmovisorTarball is the release asset name for goarch.
func CosmovisorTarball(goarch string) string {
	return "cosmovisor-" + CosmovisorVersion + "-linux-" + goarch + ".tar.gz"
}

// CosmovisorTarballURL is where the release asset for goarch is downloaded
// from. The pinned digest decides whether the bytes are accepted.
func CosmovisorTarballURL(goarch string) string {
	return "https://github.com/cosmos/cosmos-sdk/releases/download/cosmovisor%2F" + CosmovisorVersion + "/" + CosmovisorTarball(goarch)
}
