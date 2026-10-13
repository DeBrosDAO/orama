package tlsstatus

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
)

// collectTimeout bounds the whole read: one journal and a handshake per name.
const collectTimeout = 60 * time.Second

// timeLayout is how a time is printed for a person.
const timeLayout = "2006-01-02 15:04:05 UTC"

// Handle prints the certificate issuance of this node, as JSON with --json.
func Handle(out *printer.Printer) error {
	if err := clierr.RequireRoot("reading the node's certificate status (the journal of Caddy)"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()
	report, err := Collect(ctx, SystemDeps())
	if err != nil {
		return clierr.Failure("%v", err)
	}
	if out.JSONMode() {
		return out.JSON(report)
	}
	return render(out, report)
}

func render(out *printer.Printer, r *Report) error {
	if len(r.Names) == 0 {
		out.Printf("Caddy on this node manages no certificate: its Caddyfile names no HTTPS site.\n")
		return nil
	}
	if r.CA != "" {
		out.Printf("Certificate authority: %s\n\n", r.CA)
	}
	rows := make([][]string, len(r.Names))
	for i, n := range r.Names {
		rows[i] = []string{n.Name, n.state(), n.detail()}
	}
	if err := out.Table([]string{"NAME", "CERTIFICATE", "DETAIL"}, rows); err != nil {
		return err
	}
	for _, n := range r.Names {
		if n.LastError != nil {
			out.Printf("\n%s: %s\n", n.Name, n.LastError.Describe())
		}
	}
	return nil
}

func (n Name) state() string {
	if n.Held {
		return "held"
	}
	return "not held"
}

func (n Name) detail() string {
	switch {
	case n.Held:
		return fmt.Sprintf("issued by %s, expires %s", n.Issuer, n.NotAfter.Format(timeLayout))
	case n.LastError != nil && n.LastError.RateLimited():
		return "the CA refused: rate limited"
	case n.LastError != nil:
		return "the last order failed"
	case n.ServeError != "":
		return n.ServeError
	default:
		return "no certificate served and no failed order in the journal"
	}
}

// Describe is what the CA said, as a sentence an operator can act on.
func (e ObtainError) Describe() string {
	s := "the last order failed"
	if !e.At.IsZero() {
		s += " at " + e.At.Format(timeLayout)
	}
	if e.Type != "" {
		s += fmt.Sprintf(": the CA answered HTTP %d %s: %s", e.Status, e.Type, e.Message)
	} else {
		s += ": " + e.Message
	}
	if !e.RetryAfter.IsZero() {
		s += fmt.Sprintf("\nThe CA will accept a new order after %s.", e.RetryAfter.Format(timeLayout))
	}
	return s
}
