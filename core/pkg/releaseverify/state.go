package releaseverify

// seenLockSuffix names the lock file beside a record that is replaced by
// rename (the rollback record, the staged-release record): the record itself
// cannot carry the lock.
const seenLockSuffix = ".lock"

// NodeStatePaths are the files a node keeps about its release root: the root,
// the rollback record, the staged-release record and the locks beside them.
// A wiped node removes them, so it does not carry its old cluster's trust.
func NodeStatePaths() []string {
	return []string{
		RootPath,
		SeenPath, SeenPath + seenLockSuffix,
		StagedPath, StagedPath + seenLockSuffix,
	}
}
