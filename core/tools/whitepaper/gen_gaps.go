package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// knownGapsSection is the chapter section Appendix H collects.
const knownGapsSection = "Known gaps"

// genKnownGaps renders Appendix H: every chapter's Known gaps section, in
// book order, so the register of what is incomplete or wrong is one page.
func genKnownGaps(b *Book) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString(generatedHeader(titleOf(b, "appendices/h-known-gaps.md"), "the Known gaps section of every chapter"))
	sb.WriteString("Every gap the chapters record, in book order. A gap is something that is missing, incomplete or wrong in the code today; each entry is explained in its chapter.\n")
	for _, ch := range b.Chapters() {
		if ch.Template != templateSubsystem {
			continue
		}
		raw, err := os.ReadFile(b.Path(ch.File))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", ch.File, err)
		}
		body := sectionBody(splitLines(string(raw)), knownGapsSection)
		if strings.TrimSpace(body) == "" {
			continue
		}
		link := "../" + filepath.ToSlash(ch.File) + "#" + githubSlug(knownGapsSection)
		fmt.Fprintf(&sb, "\n## %d. %s\n\nFrom [%s](%s).\n\n%s\n", ch.Number, ch.Title, ch.Title, link, rebaseLinks(body, ch.File))
	}
	return []byte(sb.String()), nil
}

// sectionBody returns the text under a level-2 heading, up to the next one.
// Level-3 and deeper headings inside it are demoted to bold lines so they do
// not collide with the appendix's own headings.
func sectionBody(lines []mdLine, title string) string {
	var out []string
	in := false
	for _, l := range lines {
		if !l.inCode && strings.HasPrefix(l.text, "## ") {
			in = strings.TrimSpace(strings.TrimPrefix(l.text, "## ")) == title
			continue
		}
		if !in {
			continue
		}
		if !l.inCode && strings.HasPrefix(l.text, "###") {
			out = append(out, "**"+strings.TrimSpace(strings.TrimLeft(l.text, "#"))+"**")
			continue
		}
		out = append(out, l.text)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// rebaseLinks rewrites a chapter's relative links so they resolve from the
// appendices directory: same-volume chapter links and in-chapter anchors
// gain the chapter's path.
func rebaseLinks(body, chapterFile string) string {
	dir := filepath.Dir(chapterFile)
	return linkRe.ReplaceAllStringFunc(body, func(m string) string {
		sub := linkRe.FindStringSubmatch(m)
		target := sub[3]
		if strings.Contains(target, "://") {
			return m
		}
		path, frag, hasFrag := strings.Cut(target, "#")
		dest := chapterFile
		if path != "" {
			dest = filepath.ToSlash(filepath.Clean(filepath.Join(dir, path)))
		}
		newTarget := "../" + dest
		if hasFrag {
			newTarget += "#" + frag
		}
		return sub[1] + "[" + sub[2] + "](" + newTarget + ")"
	})
}
