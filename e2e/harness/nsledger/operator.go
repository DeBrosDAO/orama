package nsledger

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// httpStatus finds the "(HTTP nnn)" the CLI appends to a gateway refusal.
var httpStatus = regexp.MustCompile(`\(HTTP (\d{3})\)`)

// classify maps a failed removal to ErrNotFound (404), ErrPermanent (a 4xx
// that waiting does not change) or nil (retry). The CLI exits 1 for both a
// 404 and a 500, so the status in its output decides.
func classify(output string) error {
	m := httpStatus.FindStringSubmatch(output)
	if m == nil {
		return nil
	}
	switch code, _ := strconv.Atoi(m[1]); {
	case code == http.StatusNotFound:
		return ErrNotFound
	case code == http.StatusConflict, code == http.StatusRequestTimeout, code == http.StatusTooManyRequests:
		return nil
	case code >= 400 && code < 500:
		return ErrPermanent
	}
	return nil
}

// OperatorRemover removes namespaces with `orama cluster namespace remove`,
// as the operator cli is signed in as: it works for a namespace of any owner,
// which a user's own delete does not (a throwaway wallet is gone with its
// test). reason is recorded in the audit trail.
func OperatorRemover(cli *oramacli.Runner, reason string) Remover {
	return func(ctx context.Context, namespace string) error {
		res, err := cli.Run(ctx, "cluster", "namespace", "remove", namespace, "--reason", reason, "--force")
		if err != nil {
			return fmt.Errorf("failed to run orama cluster namespace remove %s: %w", namespace, err)
		}
		if res.Exit == 0 {
			return nil
		}
		failure := fmt.Errorf("orama cluster namespace remove %s exited %d: %s", namespace, res.Exit, strings.TrimSpace(res.Stderr))
		if kind := classify(res.Stdout + "\n" + res.Stderr); kind != nil {
			return fmt.Errorf("%w: %w", kind, failure)
		}
		return failure
	}
}
