package main

import (
	"os"
	"os/exec"
	"strings"
)

// gitLocationEnv are the variables through which git finds a repository
// without looking at the working directory. Git sets GIT_DIR (and, in a linked
// worktree, GIT_INDEX_FILE's base) for every hook it runs, so a command started
// from a pre-push hook would otherwise act on the repository being pushed, not
// on the directory it was asked about.
var gitLocationEnv = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_COMMON_DIR",
	"GIT_PREFIX",
}

// gitCommand is git run in dir, on the repository dir belongs to.
func gitCommand(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = withoutGitLocation(os.Environ())
	return cmd
}

func withoutGitLocation(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !isGitLocation(name) {
			out = append(out, kv)
		}
	}
	return out
}

func isGitLocation(name string) bool {
	for _, v := range gitLocationEnv {
		if name == v {
			return true
		}
	}
	return false
}
