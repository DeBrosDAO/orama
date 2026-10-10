package install

import (
	"fmt"
	"strings"
)

// tomlSet sets key in the [section] of a TOML file written by the chain's
// own templates (CometBFT's config.toml, the SDK's app.toml), keeping every
// other line, comment and blank as it was. value is the TOML literal to write:
// a quoted string, a number, a boolean.
//
// It edits a line that exists. The templates carry every key the installer
// sets, so a key it cannot find means the template changed under it, and that
// is an error: a sed that matched nothing left a node with a setting
// silently unset. A key written twice in one section, or a section twice, is
// refused for the same reason. Only the file's own section headers count: a
// '#' comment is skipped, and so is a key of another section with the same name.
func tomlSet(doc, section, key, value string) (string, error) {
	lines := strings.Split(doc, "\n")
	current, found := "", -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if name, ok := tomlSectionHeader(trimmed); ok {
			current = name
			continue
		}
		if current != section || !tomlKeyIs(trimmed, key) {
			continue
		}
		if found >= 0 {
			return "", fmt.Errorf("the key %q appears twice in [%s]", key, section)
		}
		found = i
	}
	if found < 0 {
		return "", fmt.Errorf("no %q key in [%s] of the file: the chain's template changed, so the installer cannot set it", key, tomlSectionLabel(section))
	}
	lines[found] = key + " = " + value
	return strings.Join(lines, "\n"), nil
}

// tomlSectionLabel renders a section name for a message; the top level has none.
func tomlSectionLabel(section string) string {
	if section == "" {
		return "the top level"
	}
	return section
}

// tomlSectionHeader reports the section a "[name]" line opens. An array of
// tables ("[[name]]") is a section too: keys under it are not the table's own.
func tomlSectionHeader(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	name := strings.TrimSpace(strings.Trim(trimmed, "[]"))
	if i := strings.Index(name, "#"); i >= 0 {
		name = strings.TrimSpace(name[:i])
	}
	if strings.HasPrefix(trimmed, "[[") {
		return "[" + name + "]", true
	}
	return name, true
}

// tomlKeyIs reports whether a trimmed line assigns key.
func tomlKeyIs(trimmed, key string) bool {
	if strings.HasPrefix(trimmed, "#") {
		return false
	}
	name, _, ok := strings.Cut(trimmed, "=")
	return ok && strings.TrimSpace(name) == key
}
