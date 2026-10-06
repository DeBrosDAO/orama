package manifest

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Shapes of manifest fields. Each covers kind has one, so an entry that could
// never match an enumerated item is refused at load time, not reported later
// as "uncovered" next to the real thing.
var (
	idShape     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$`)
	areaShape   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	cliShape    = regexp.MustCompile(`^orama( [a-z0-9][a-z0-9-]*)+$`)
	routeShape  = regexp.MustCompile(`^/[A-Za-z0-9._~/{}-]*$`)
	msgShape    = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*\.Msg[A-Z][A-Za-z0-9]*$`)
	queryShape  = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*\.[A-Z][A-Za-z0-9]*$`)
	unitShape   = regexp.MustCompile(`^orama[a-z0-9-]*(@[a-z0-9-]*)?\.(service|timer)$`)
	configShape = regexp.MustCompile(`^[a-z0-9_./-]+\.(yaml|yml|json|conf|toml|env):[A-Za-z0-9_.-]+$`)
	claimShape  = regexp.MustCompile(`^(docs|plans)/[A-Za-z0-9_./-]+\.md(#[a-z0-9-]+)?(: \S.*)?$`)
)

// Validate checks a manifest and returns every problem at once.
func (m *Manifest) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if !idShape.MatchString(m.ID) {
		bad("id %q must be lowercase letters, digits and hyphens", m.ID)
	}
	if m.Dir != "" && m.Dir != m.ID {
		bad("id %q does not match its directory %q", m.ID, m.Dir)
	}
	if strings.TrimSpace(m.Title) == "" {
		bad("title is required")
	}
	if !areaShape.MatchString(m.Area) {
		bad("area %q must be a lowercase word", m.Area)
	}
	if m.Stage < MinStage || m.Stage > MaxStage {
		bad("stage %d is outside %d..%d", m.Stage, MinStage, MaxStage)
	}
	if m.Requires.ExtraNodes < 0 || m.Requires.ExtraNodes > MaxExtraNodes {
		bad("requires.extra_nodes %d is outside 0..%d", m.Requires.ExtraNodes, MaxExtraNodes)
	}
	errs = append(errs, validateSubtasks(m.Subtasks)...)
	errs = append(errs, m.Covers.validate()...)
	return errors.Join(errs...)
}

func validateSubtasks(ids []int) []error {
	var errs []error
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 {
			errs = append(errs, fmt.Errorf("subtask id %d must be a positive bugboard id", id))
		}
		if seen[id] {
			errs = append(errs, fmt.Errorf("subtask %d is listed twice", id))
		}
		seen[id] = true
	}
	return errs
}

func (c Covers) validate() []error {
	kinds := []struct {
		name    string
		entries []string
		shape   *regexp.Regexp
	}{
		{"cli", c.CLI, cliShape},
		{"routes", c.Routes, routeShape},
		{"msgs", c.Msgs, msgShape},
		{"queries", c.Queries, queryShape},
		{"units", c.Units, unitShape},
		{"config", c.Config, configShape},
		{"claims", c.Claims, claimShape},
	}
	var errs []error
	total := 0
	for _, k := range kinds {
		seen := map[string]bool{}
		for _, e := range k.entries {
			total++
			if !k.shape.MatchString(e) {
				errs = append(errs, fmt.Errorf("covers.%s entry %q is malformed (want %s)", k.name, e, k.shape))
			}
			if seen[e] {
				errs = append(errs, fmt.Errorf("covers.%s entry %q is listed twice", k.name, e))
			}
			seen[e] = true
		}
	}
	if total == 0 {
		errs = append(errs, errors.New("covers is empty: a feature must say what it tests"))
	}
	return errs
}
