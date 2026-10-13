package install

import "strings"

// anywhereOn starts the "To" column of a rule bound to an interface and to no
// port: `ufw allow in on wg0 ...` shows as "Anywhere on wg0".
const anywhereOn = "Anywhere on "

// allowRow is one allow rule `ufw status` reports, in the argument form
// `ufw allow` takes, with its comment ("" when it has none).
type allowRow struct {
	rule    string
	comment string
}

// parseAllowRows extracts the IPv4 allow rules from `ufw status` output.
//
// The output looks like:
//
//	To                         Action      From
//	--                         ------      ----
//	22/tcp                     ALLOW       Anywhere                   # orama
//	Anywhere on tailscale0     ALLOW       Anywhere
//	Anywhere on wg0            ALLOW       10.0.0.0/24                # orama
//	9001/tcp                   ALLOW       Anywhere                   # Anon ORPort
//	22/tcp (v6)                ALLOW       Anywhere (v6)              # orama
//
// Each row is normalised back into the argument form `ufw allow` takes, so a
// live rule can be compared against a desired one by string equality.
//
// v6 rows are skipped: IPv6 is disabled at the kernel level on these nodes, ufw
// mirrors every v4 rule into one, and `ufw delete allow <rule>` removes both.
// An interface row with no port ("Anywhere on wg0") reads back as
// "in on wg0 [from <src>]", which is how Orama admits the mesh. An interface
// row with a port is skipped: Orama adds none, and reading one as a port made
// every reconcile fail on a node running tailscale. An untagged interface row
// such as "Anywhere on tailscale0" parses and is left alone like any other
// operator rule.
func parseAllowRows(status string) []allowRow {
	var rows []allowRow
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "(v6)") {
			continue
		}
		comment := ""
		if i := strings.Index(line, "# "); i >= 0 {
			comment = strings.TrimSpace(line[i+len("# "):])
			line = strings.TrimSpace(line[:i])
		}

		// "To  ACTION  From", with the action as the anchor.
		idx := strings.Index(line, "ALLOW")
		if idx < 0 {
			continue
		}
		to := strings.TrimSpace(line[:idx])
		from := strings.TrimSpace(line[idx+len("ALLOW"):])
		from = strings.TrimSpace(strings.TrimPrefix(from, "IN"))
		if to == "" || from == "" || strings.Contains(from, " on ") {
			continue
		}

		var rule string
		switch {
		case strings.HasPrefix(to, anywhereOn):
			// `ufw allow in on wg0 from 10.0.0.0/24`
			iface := strings.TrimPrefix(to, anywhereOn)
			if iface == "" || strings.Contains(iface, " ") {
				continue
			}
			rule = "in on " + iface
			if from != "Anywhere" {
				rule += " from " + from
			}
		case strings.Contains(to, " on "):
			// A port on an interface; Orama adds none, and the shape does
			// not normalise into `ufw allow` arguments.
			continue
		case from == "Anywhere":
			// `ufw allow 22/tcp`
			rule = to
		case to == "Anywhere":
			// `ufw allow from 10.0.0.0/24`
			rule = "from " + from
		default:
			// `ufw allow from <src> to any port <port>`
			rule = "from " + from + " to any port " + to
		}
		rows = append(rows, allowRow{rule: rule, comment: comment})
	}
	return rows
}

// ownedAllowRules are the rules among rows that Orama tagged.
func ownedAllowRules(rows []allowRow) []string {
	var rules []string
	for _, r := range rows {
		if r.comment == ownedRuleComment {
			rules = append(rules, r.rule)
		}
	}
	return rules
}

// parseOwnedAllowRules extracts the allow rules Orama tagged from `ufw status`
// output. Untagged rows are not Orama's to remove; a comment that merely
// contains the tag is someone else's.
func parseOwnedAllowRules(status string) []string {
	return ownedAllowRules(parseAllowRows(status))
}
