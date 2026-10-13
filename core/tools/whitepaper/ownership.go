package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// tooBroad are prefixes a chapter may not own outright: owning one would
// silently adopt every future package under it, which is what the
// ownership gate exists to catch.
var tooBroad = map[string]bool{
	"":          true,
	"core/":     true,
	"core/pkg/": true,
	"core/cmd/": true,
	"chain/":    true,
	"chain/x/":  true,
	"sdk/src/":  true,
}

// unownedGroupDepth is how many path segments an unowned file is reported
// under, so one new package is one line, not one line per file.
const unownedGroupDepth = 3

// trackedFiles lists the repository's tracked files.
func trackedFiles(root string) ([]string, error) {
	cmd := gitCommand(root, "ls-files", "-z")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list tracked files with git ls-files in %s: %w: %s", root, err, stderr.String())
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// matchesPrefix reports whether file falls under an owns/exclude entry. An
// entry ending in "/" is a directory; anything else names one file.
func matchesPrefix(file, entry string) bool {
	if strings.HasSuffix(entry, "/") {
		return strings.HasPrefix(file, entry)
	}
	return file == entry
}

// checkOwnership is the ownership gate: every owns entry is valid and
// unique, and every tracked file outside `exclude` has an owner.
func checkOwnership(b *Book, files []string) []problem {
	var probs []problem
	owners := map[string]string{}
	for _, ch := range b.Chapters() {
		for _, entry := range ch.Owns {
			probs = append(probs, validateOwnsEntry(ch, entry, files, owners)...)
		}
	}
	unowned := map[string]int{}
	for _, f := range files {
		if anyPrefix(f, b.Manifest.Exclude) || owned(f, b) {
			continue
		}
		unowned[groupPath(f)]++
	}
	keys := make([]string, 0, len(unowned))
	for k := range unowned {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		probs = append(probs, problem{gate: "ownership", file: "book.yaml",
			msg: fmt.Sprintf("%s (%d tracked files) is explained by no chapter: add it to a chapter's owns, or to exclude", k, unowned[k])})
	}
	return probs
}

func validateOwnsEntry(ch *Chapter, entry string, files []string, owners map[string]string) []problem {
	var probs []problem
	if tooBroad[entry] {
		probs = append(probs, problem{gate: "ownership", file: ch.File,
			msg: fmt.Sprintf("owns %q, which is too broad: own the packages under it", entry)})
	}
	if prev, dup := owners[entry]; dup {
		probs = append(probs, problem{gate: "ownership", file: ch.File,
			msg: fmt.Sprintf("owns %q, already owned by %s", entry, prev)})
	}
	owners[entry] = ch.File
	if !anyFileUnder(entry, files) {
		probs = append(probs, problem{gate: "ownership", file: ch.File,
			msg: fmt.Sprintf("owns %q, which matches no tracked file", entry)})
	}
	return probs
}

func owned(file string, b *Book) bool {
	for _, ch := range b.Chapters() {
		if anyPrefix(file, ch.Owns) {
			return true
		}
	}
	return false
}

// ownerOf returns the chapter owning file by the longest matching entry, or
// nil when no chapter owns it.
func ownerOf(file string, b *Book) *Chapter {
	var best *Chapter
	bestLen := -1
	for _, ch := range b.Chapters() {
		for _, entry := range ch.Owns {
			if matchesPrefix(file, entry) && len(entry) > bestLen {
				best, bestLen = ch, len(entry)
			}
		}
	}
	return best
}

func anyPrefix(file string, entries []string) bool {
	for _, e := range entries {
		if matchesPrefix(file, e) {
			return true
		}
	}
	return false
}

func anyFileUnder(entry string, files []string) bool {
	for _, f := range files {
		if matchesPrefix(f, entry) {
			return true
		}
	}
	return false
}

func groupPath(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) <= unownedGroupDepth {
		return file
	}
	return strings.Join(parts[:unownedGroupDepth], "/") + "/"
}
