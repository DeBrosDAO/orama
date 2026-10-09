package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// diagramDir is where D2 sources and their rendered SVGs live, book-relative.
const diagramDir = "diagrams"

// d2Args are the fixed render flags: ELK layout, neutral theme, no sketch,
// so every machine renders the same picture.
var d2Args = []string{"--layout", "elk", "--theme", "0"}

// stampPrefix marks the source hash appended to each rendered SVG. The gate
// compares it with the D2 source, so it needs no d2 binary to run.
const stampPrefix = "<!-- d2-source-sha256:"

var stampRe = regexp.MustCompile(`<!-- d2-source-sha256:([0-9a-f]{64}) -->\s*$`)

func diagramSources(b *Book) ([]string, error) {
	srcs, err := filepath.Glob(b.Path(filepath.Join(diagramDir, "*.d2")))
	if err != nil {
		return nil, fmt.Errorf("failed to list diagram sources: %w", err)
	}
	sort.Strings(srcs)
	return srcs, nil
}

func sourceHash(src string) (string, error) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("failed to read diagram source %s: %w", src, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func svgPath(src string) string { return strings.TrimSuffix(src, ".d2") + ".svg" }

// renderDiagrams renders every D2 source whose SVG is missing or stale, and
// stamps the source hash into the SVG.
func renderDiagrams(b *Book) error {
	srcs, err := diagramSources(b)
	if err != nil {
		return err
	}
	for _, src := range srcs {
		want, err := sourceHash(src)
		if err != nil {
			return err
		}
		if got, _ := svgStamp(svgPath(src)); got == want {
			continue
		}
		if err := renderOne(src, want); err != nil {
			return err
		}
		fmt.Printf("rendered %s\n", filepath.Base(svgPath(src)))
	}
	return nil
}

func renderOne(src, hash string) error {
	out := svgPath(src)
	args := append(append([]string{}, d2Args...), src, out)
	cmd := exec.Command("d2", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to render %s with d2 (is d2 installed? brew install d2): %w: %s", src, err, stderr.String())
	}
	f, err := os.OpenFile(out, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open %s to stamp it: %w", out, err)
	}
	if _, err := fmt.Fprintf(f, "\n%s%s -->\n", stampPrefix, hash); err != nil {
		f.Close()
		return fmt.Errorf("failed to stamp %s: %w", out, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close %s: %w", out, err)
	}
	return nil
}

// svgStamp reads the source hash stamped into a rendered SVG.
func svgStamp(svg string) (string, error) {
	raw, err := os.ReadFile(svg)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", svg, err)
	}
	m := stampRe.FindSubmatch(raw)
	if m == nil {
		return "", fmt.Errorf("%s carries no d2 source stamp", filepath.Base(svg))
	}
	return string(m[1]), nil
}

// checkDiagrams is the diagram gate: every source has an up-to-date SVG,
// every SVG has a source, and every diagram is used by some document.
func checkDiagrams(b *Book, used map[string]bool) []problem {
	srcs, err := diagramSources(b)
	if err != nil {
		return []problem{{gate: "diagrams", file: diagramDir, msg: err.Error()}}
	}
	var probs []problem
	for _, src := range srcs {
		probs = append(probs, checkOneDiagram(b, src, used)...)
	}
	svgs, _ := filepath.Glob(b.Path(filepath.Join(diagramDir, "*.svg")))
	for _, svg := range svgs {
		if !fileExists(strings.TrimSuffix(svg, ".svg") + ".d2") {
			probs = append(probs, problem{gate: "diagrams", file: rel(b, svg), msg: "has no D2 source; diagrams are drawn in D2, never by hand"})
		}
	}
	return probs
}

func checkOneDiagram(b *Book, src string, used map[string]bool) []problem {
	file := rel(b, src)
	want, err := sourceHash(src)
	if err != nil {
		return []problem{{gate: "diagrams", file: file, msg: err.Error()}}
	}
	var probs []problem
	got, err := svgStamp(svgPath(src))
	switch {
	case err != nil:
		probs = append(probs, problem{gate: "diagrams", file: file, msg: err.Error() + ": run make whitepaper-diagrams"})
	case got != want:
		probs = append(probs, problem{gate: "diagrams", file: file, msg: "the SVG is stale: run make whitepaper-diagrams"})
	}
	if !used[filepath.Base(svgPath(src))] {
		probs = append(probs, problem{gate: "diagrams", file: file, msg: "no document embeds this diagram"})
	}
	return probs
}

func rel(b *Book, abs string) string {
	r, err := filepath.Rel(b.Dir(), abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}
