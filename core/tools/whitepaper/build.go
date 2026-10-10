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

// pdfGroups maps every book file to the PDF it is typeset into: one per
// volume, and one for all the appendices.
func pdfGroups(b *Book) map[string]string {
	groups := map[string]string{}
	if single(b) {
		for file := range fileKeys(b) {
			groups[file] = "book"
		}
		return groups
	}
	for _, vol := range b.Manifest.Volumes {
		for _, part := range vol.Parts {
			for _, ch := range part.Chapters {
				groups[ch.File] = fmt.Sprintf("vol%d", vol.Number)
			}
		}
	}
	for _, a := range b.Manifest.Appendices {
		groups[a.File] = "appendices"
	}
	return groups
}

// sameDocument narrows the label map to the files typeset into the same PDF
// as file: a link to another PDF has no label to land on, so the filter
// leaves its text unlinked.
func sameDocument(keys, groups map[string]string, file string) map[string]string {
	out := map[string]string{}
	for f, key := range keys {
		if groups[f] == groups[file] {
			out[f] = key
		}
	}
	return out
}

func writeBuildInputs(b *Book) error {
	if err := os.WriteFile(b.Path(filepath.Join(buildDir, "filter.lua")), luaFilter, 0o644); err != nil {
		return fmt.Errorf("failed to write the pandoc filter: %w", err)
	}
	keys := fileKeys(b)
	groups := pdfGroups(b)
	root := typstRoot(b)
	prefix, err := filepath.Rel(root, b.Dir())
	if err != nil {
		return fmt.Errorf("failed to relate the book to the typst root %s: %w", root, err)
	}
	if prefix == "." {
		prefix = ""
	}
	for file, key := range keys {
		fields := map[string]any{"self": file, "key": key, "filemap": sameDocument(keys, groups, file),
			"root": root, "rootprefix": filepath.ToSlash(prefix)}
		for k, v := range b.Manifest.Typeset.filterMeta() {
			fields[k] = v
		}
		meta, err := yaml.Marshal(fields)
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

// single reports whether the book builds into one PDF with no appendices
// volume.
func single(b *Book) bool { return b.Manifest.Output != "" }

// typstRoot is the directory typst resolves root-relative paths against: the
// book directory, widened to the common ancestor of the book and its
// template when the template lives in another book.
func typstRoot(b *Book) string {
	root := b.Dir()
	tmplDir := filepath.Dir(b.TemplatePath())
	for {
		r, err := filepath.Rel(root, tmplDir)
		if err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return root
		}
		root = filepath.Dir(root)
	}
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
	return prependImport(b, b.Path(filepath.Join(buildDir, key+".typ")))
}

// templateImport is the template's path as written in a build file, which
// sits in the book's build directory.
func templateImport(b *Book) (string, error) {
	r, err := filepath.Rel(b.Path(buildDir), b.TemplatePath())
	if err != nil {
		return "", fmt.Errorf("failed to relate the typst template to the build directory: %w", err)
	}
	return filepath.ToSlash(r), nil
}

// prependImport gives an included chapter the template helpers the filter
// emits: a Typst include does not see the including file's imports.
func prependImport(b *Book, path string) error {
	tmpl, err := templateImport(b)
	if err != nil {
		return err
	}
	chapterImport := fmt.Sprintf("#import %q: glance\n", tmpl)
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
	if single(b) {
		v := typstVolume{name: "book", title: b.Manifest.Subtitle, firstChapter: 1}
		for _, vol := range b.Manifest.Volumes {
			for _, part := range vol.Parts {
				v.body = append(v.body, fmt.Sprintf("#part(%q)", part.Title))
				for _, ch := range part.Chapters {
					v.body = append(v.body, fmt.Sprintf("#include %q", keys[ch.File]+".typ"))
				}
			}
		}
		return []typstVolume{v}
	}
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
	tmpl, err := templateImport(b)
	if err != nil {
		return err
	}
	main := fmt.Sprintf(`#import %q: *
#show: book.with(title: %q, subtitle: %q, volume: %q, version: %q, commit: %q, first-chapter: %d, mode: %q, source: %q%s)
%s
`, tmpl, b.Manifest.Title, b.Manifest.Subtitle, v.title, b.Manifest.Version, commit, v.firstChapter, mode, b.RelDir()+"/", b.Manifest.Typeset.templateArgs(), strings.Join(v.body, "\n"))
	src := b.Path(filepath.Join(buildDir, v.name+".typ"))
	if err := os.WriteFile(src, []byte(main), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", src, err)
	}
	name := fmt.Sprintf("orama-whitepaper-technical-reference-v%s-%s.pdf", b.Manifest.Version, v.name)
	if single(b) {
		name = fmt.Sprintf("%s-v%s.pdf", b.Manifest.Output, b.Manifest.Version)
	}
	out := b.Path(filepath.Join(distDir, name))
	cmd := exec.Command("typst", "compile", "--root", typstRoot(b), src, out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("typst failed on %s: %w: %s", v.name, err, stderr.String())
	}
	fmt.Printf("built %s\n", rel(b, out))
	if single(b) {
		printPageCount(out)
	}
	return nil
}

// printPageCount reports the PDF's page count with pdfinfo. A missing
// pdfinfo is reported, not fatal: the PDF itself is built.
func printPageCount(pdf string) {
	out, err := exec.Command("pdfinfo", pdf).Output()
	if err != nil {
		fmt.Printf("pages: unknown (pdfinfo failed: %v; brew install poppler)\n", err)
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		if n, ok := strings.CutPrefix(line, "Pages:"); ok {
			fmt.Printf("pages: %s\n", strings.TrimSpace(n))
			return
		}
	}
	fmt.Println("pages: unknown (pdfinfo printed no page count)")
}

// gitCommit is the short commit the book was built from, printed on the
// title page.
func gitCommit(root string) (string, error) {
	cmd := gitCommand(root, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to read the commit with git rev-parse in %s: %w", root, err)
	}
	return strings.TrimSpace(string(out)), nil
}
