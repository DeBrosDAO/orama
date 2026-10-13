package tlsstatus

import (
	"slices"
	"strconv"
	"strings"
)

// defaultHTTPSPort is the port Caddy serves HTTPS on unless the Caddyfile's
// global https_port says otherwise (it does behind the SNI router).
const defaultHTTPSPort = 443

// caddyfile is what this command needs from the node's Caddyfile.
type caddyfile struct {
	// ACMECA is the global acme_ca: the directory every issuer asks.
	ACMECA string
	// HTTPSPort is where Caddy serves HTTPS.
	HTTPSPort int
	// Names are the sites Caddy manages certificates for, in file order: the
	// HTTPS sites. The plain-HTTP sites and the :80 catch-all have none.
	Names []string
}

// parseCaddyfile reads the Caddyfile the installer writes
// (pkg/install/installers generateCaddyfile): one global block, then one block
// per site, each opened by a line ending in "{" at the top level.
func parseCaddyfile(text string) caddyfile {
	cf := caddyfile{HTTPSPort: defaultHTTPSPort}
	depth, global := 0, false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case depth == 0 && strings.HasSuffix(line, "{"):
			addr := strings.TrimSpace(strings.TrimSuffix(line, "{"))
			global = addr == ""
			if !global {
				cf.addSites(addr)
			}
			depth++
		case depth == 1 && global && line != "}":
			cf.global(line)
			depth += strings.Count(line, "{") - strings.Count(line, "}")
		default:
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth <= 0 {
				depth, global = 0, false
			}
		}
	}
	return cf
}

// global reads one directive of the global block.
func (cf *caddyfile) global(line string) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}
	switch fields[0] {
	case "acme_ca":
		cf.ACMECA = fields[1]
	case "https_port":
		if port, err := strconv.Atoi(fields[1]); err == nil && port > 0 {
			cf.HTTPSPort = port
		}
	}
}

// addSites adds the HTTPS site names of one site address line: a plain-HTTP
// address (http://...), a bare port (:80) and a snippet name ((name)) are not
// names Caddy obtains a certificate for.
func (cf *caddyfile) addSites(addr string) {
	for _, a := range strings.FieldsFunc(addr, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		a = strings.TrimPrefix(a, "https://")
		if a == "" || strings.HasPrefix(a, "http://") || strings.HasPrefix(a, ":") || strings.HasPrefix(a, "(") {
			continue
		}
		if !slices.Contains(cf.Names, a) {
			cf.Names = append(cf.Names, a)
		}
	}
}
