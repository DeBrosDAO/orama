package constants

// Pinned SHA-256 digests of the release tarballs the build downloads, by GOARCH
// (linux). A download whose digest differs is refused, so the bytes packed into
// the archive are the release the version constant names. Update them with the
// version: a Kubo digest is the SHA-256 of the tarball whose upstream
// <tarball>.sha512 was checked; an rqlite digest is the one the release page
// reports for the asset.

// IPFSKuboTarballSHA256 is the digest of kubo_<IPFSKuboVersion>_linux-<arch>.tar.gz.
var IPFSKuboTarballSHA256 = map[string]string{
	"amd64": "3f2bf974ab2a3ec6d997fac7d8cb46f59983a7cddd0b55ef998e6ce379155fb2",
	"arm64": "e09237abadd9578d7a73dd2e2b718848b78437cb56e48cc9742b23ce9ebac829",
}

// RQLiteTarballSHA256 is the digest of rqlite-v<RQLiteVersion>-linux-<arch>.tar.gz.
var RQLiteTarballSHA256 = map[string]string{
	"amd64": "a9de16c18e7eadf466aacfd1cb66791fd4283cf60db58c15b9fd52ab2e80512b",
	"arm64": "f8ece5a9778ebff09f0e055cc08f2270c7bba86b32f23b8404e0e5bed1a9b3c7",
}
