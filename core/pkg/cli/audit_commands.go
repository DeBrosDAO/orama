package cli

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// The audit trail from a terminal.
//
// The events are written to a table only the gateway can reach, so "who minted
// this key", "who deleted that deployment" and "when did this wallet last sign
// in" were questions with no answer outside the database. This is the reader.

const (
	// auditFollowInterval is how often --follow asks for what is new. These
	// events are made by people at human pace; polling faster only costs the
	// gateway a query.
	auditFollowInterval = 5 * time.Second

	// auditDefaultLimit matches the gateway's own page size.
	auditDefaultLimit = 50
)

// auditColumns is the header the trail is printed under, and the keys a JSON
// reader sees.
var auditColumns = []string{"TIME", "ACTION", "RESULT", "ACTOR", "RESOURCE", "IP", "DETAILS"}

// AuditFilter is what the caller asked to see.
type AuditFilter struct {
	Namespace string
	Action    string
	Principal string
	Since     string
	Limit     int
	Follow    bool
}

// AuditList prints a namespace's audit trail, oldest first, and keeps printing
// when Follow is set.
func AuditList(out *printer.Printer, filter AuditFilter) error {
	if filter.Action != "" && !auth.IsAuditAction(filter.Action) {
		return clierr.Usage("unknown action %q. Known actions: %s",
			filter.Action, strings.Join(auth.AuditActions, ", "))
	}

	gatewayURL, apiKey, err := loadAuthForNamespace(filter.Namespace)
	if err != nil {
		return err
	}
	if filter.Limit <= 0 {
		filter.Limit = auditDefaultLimit
	}

	rows, newest, err := fetchAuditPage(gatewayURL, apiKey, filter)
	if err != nil {
		return err
	}
	if len(rows) == 0 && !filter.Follow && !out.JSONMode() {
		out.Printf("No audit events.\n")
		return nil
	}
	if err := out.Table(auditColumns, rows); err != nil {
		return err
	}
	if !filter.Follow {
		return nil
	}

	// Follow from the newest row already printed, so nothing is repeated and
	// nothing written in between is skipped.
	since := filter.Since
	if newest != "" {
		since = newest
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(auditFollowInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		next := filter
		next.Since = since
		rows, newest, err := fetchAuditPage(gatewayURL, apiKey, next)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			continue
		}
		if err := out.Rows(auditColumns, rows); err != nil {
			return err
		}
		since = newest
	}
}

// fetchAuditPage returns one page as printable rows, oldest first, and the
// created_at of the newest row in it.
//
// The gateway answers newest first because that is what a page limit should
// keep; a trail reads the other way round.
func fetchAuditPage(gatewayURL, apiKey string, filter AuditFilter) ([][]string, string, error) {
	query := url.Values{}
	query.Set("limit", strconv.Itoa(filter.Limit))
	if filter.Action != "" {
		query.Set("action", filter.Action)
	}
	if filter.Principal != "" {
		query.Set("principal", filter.Principal)
	}
	if filter.Since != "" {
		query.Set("since", filter.Since)
	}

	result, err := nsRequest("read the audit trail", http.MethodGet,
		gatewayURL+"/v1/audit?"+query.Encode(), apiKey, nil)
	if err != nil {
		return nil, "", err
	}

	raw, _ := result["events"].([]any)
	rows := make([][]string, 0, len(raw))
	newest := ""
	for i := len(raw) - 1; i >= 0; i-- {
		entry, ok := raw[i].(map[string]any)
		if !ok {
			continue
		}
		createdAt := auditString(entry, "created_at")
		if createdAt > newest {
			newest = createdAt
		}
		rows = append(rows, []string{
			createdAt,
			auditString(entry, "action"),
			auditString(entry, "result"),
			auditString(entry, "actor"),
			auditString(entry, "resource"),
			auditString(entry, "ip"),
			auditString(entry, "metadata"),
		})
	}
	return rows, newest, nil
}

func auditString(entry map[string]any, key string) string {
	s, _ := entry[key].(string)
	return s
}
