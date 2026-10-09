package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed filter.lua
var luaFilter []byte

// Build output directories, book-relative. Both are git-ignored.
const (
	buildDir = ".build"
	distDir  = "dist"
)

// typstPart is one part divider and its chapters, for the volume template.
type typstVolume struct {
	name         string // file stem: vol1, vol2, appendices
	title        string
	firstChapter int
	body         []string // Typst lines: #part(...) and #include(...)
}

// buildBook typesets each volume: every Markdown file goes through pandoc
// with the book filter to Typst, then typst compiles the volume.
func buildBook(b *Book) error {
	for _, tool := range []string{"pandoc", "typst"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s is not installed (brew install %s): %w", tool, tool, err)
		}
	}
	for _, d := range []string{buildDir, distDir} {
		if err := os.MkdirAll(b.Path(d), 0o755); err != nil {
			return fmt.Errorf("failed to create %s: %w", d, err)
		}
	}
	if err := writeBuildInputs(b); err != nil {
		return err
	}
	for _, v := range volumes(b) {
		if err := buildVolume(b, v); err != nil {
			return err
		}
	}
	return nil
}

// fileKeys maps every book file to its Typst label prefix.
func fileKeys(b *Book) map[string]string {
	keys := map[string]string{}
	for _, ch := range b.Chapters() {
		keys[ch.File] = fmt.Sprintf("ch%02d", ch.Number)
	}
	for _, a := range b.Manifest.Appendices {
		keys[a.File] = "app" + a.Letter
	}
	return keys
}

func writeBuildInputs(b *Book) error {
	if err := os.WriteFile(b.Path(filepath.Join(buildDir, "filter.lua")), luaFilter, 0o644); err != nil {
		return fmt.Errorf("failed to write the pandoc filter: %w", err)
	}
	keys := fileKeys(b)
	for file, key := range keys {
		meta, err := yaml.Marshal(map[string]any{"self": file, "key": key, "filemap": keys})
		if err != nil {
			return fmt.Errorf("failed to encode build metadata for %s: %w", file, err)
		}
		if err := os.WriteFile(b.Path(filepath.Join(buildDir, key+".meta.yaml")), meta, 0o644); err != nil {
			return fmt.Errorf("failed to write build metadata for %s: %w", file, err)
		}
		if err := pandoc(b, file, key); err != nil {
			return err
		}
	}
	return nil
}

// pandoc converts one book file to Typst.
func pandoc(b *Book, file, key string) error {
	if !fileExists(b.Path(file)) {
		return fmt.Errorf("%s is listed in book.yaml but missing", file)
	}
	cmd := exec.Command("pandoc",
		"--from", "gfm+implicit_figures",
		"--to", "typst",
		"--resource-path", b.Dir(),
		"--lua-filter", b.Path(filepath.Join(buildDir, "filter.lua")),
		"--metadata-file", b.Path(filepath.Join(buildDir, key+".meta.yaml")),
		"--output", b.Path(filepath.Join(buildDir, key+".typ")),
		b.Path(file))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pandoc failed on %s: %w: %s", file, err, stderr.String())
	}
	return prependImport(b.Path(filepath.Join(buildDir, key+".typ")))
}

// chapterImport gives each included chapter the template helpers the filter
// emits: a Typst include does not see the including file's imports.
const chapterImport = "#import \"../typst/template.typ\": glance\n"

func prependImport(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	if err := os.WriteFile(path, append([]byte(chapterImport), raw...), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

func volumes(b *Book) []typstVolume {
	keys := fileKeys(b)
	var out []typstVolume
	n := 1
	for _, vol := range b.Manifest.Volumes {
		v := typstVolume{name: fmt.Sprintf("vol%d", vol.Number), title: vol.Title, firstChapter: n}
		for _, part := range vol.Parts {
			v.body = append(v.body, fmt.Sprintf("#part(%q)", part.Title))
			for _, ch := range part.Chapters {
				v.body = append(v.body, fmt.Sprintf("#include %q", keys[ch.File]+".typ"))
				n++
			}
		}
		out = append(out, v)
	}
	app := typstVolume{name: "appendices", title: "Appendices", firstChapter: 1}
	for _, a := range b.Manifest.Appendices {
		app.body = append(app.body, fmt.Sprintf("#include %q", keys[a.File]+".typ"))
	}
	return append(out, app)
}

func buildVolume(b *Book, v typstVolume) error {
	commit, err := gitCommit(b.Root)
	if err != nil {
		return err
	}
	mode := "chapters"
	if v.name == "appendices" {
		mode = "appendices"
	}
	main := fmt.Sprintf(`#import "../typst/template.typ": *
#show: book.with(title: %q, subtitle: %q, volume: %q, version: %q, commit: %q, first-chapter: %d, mode: %q)
%s
`, b.Manifest.Title, b.Manifest.Subtitle, v.title, b.Manifest.Version, commit, v.firstChapter, mode, strings.Join(v.body, "\n"))
	src := b.Path(filepath.Join(buildDir, v.name+".typ"))
	if err := os.WriteFile(src, []byte(main), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", src, err)
	}
	out := b.Path(filepath.Join(distDir, fmt.Sprintf("orama-whitepaper-technical-reference-v%s-%s.pdf", b.Manifest.Version, v.name)))
	cmd := exec.Command("typst", "compile", "--root", b.Dir(), src, out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("typst failed on %s: %w: %s", v.name, err, stderr.String())
	}
	fmt.Printf("built %s\n", rel(b, out))
	return nil
}

// gitCommit is the short commit the book was built from, printed on the
// title page.
func gitCommit(root string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to read the commit with git rev-parse in %s: %w", root, err)
	}
	return strings.TrimSpace(string(out)), nil
}
