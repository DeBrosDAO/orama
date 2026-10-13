package tlsstatus

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/termtext"
)

// Caddy's JSON log: the loggers that obtain and renew certificates, and the
// messages that end an attempt.
const (
	loggerObtain = "tls.obtain"
	loggerRenew  = "tls.renew"

	msgObtained = "certificate obtained successfully"
	msgRenewed  = "certificate renewed successfully"

	levelError = "error"
)

// logLine is the part of a Caddy JSON log line this command reads.
type logLine struct {
	Level      string  `json:"level"`
	Ts         float64 `json:"ts"`
	Logger     string  `json:"logger"`
	Msg        string  `json:"msg"`
	Identifier string  `json:"identifier"`
	Issuer     string  `json:"issuer"`
	Error      string  `json:"error"`
}

// lastObtainErrors reads Caddy's journal and returns, for each name, the last
// time obtaining or renewing its certificate failed, unless a success came
// after it. Lines that are not Caddy's JSON (systemd's, a truncated line, the
// journal's "-- No entries --") are skipped: the journal holds more than the
// log. ca fills the error's CA, which the log line does not carry as a
// directory (it names the issuer by a key).
func lastObtainErrors(journal, ca string) map[string]ObtainError {
	out := map[string]ObtainError{}
	for _, raw := range strings.Split(journal, "\n") {
		start := strings.IndexByte(raw, '{')
		if start < 0 {
			continue
		}
		var l logLine
		if err := json.Unmarshal([]byte(raw[start:]), &l); err != nil {
			continue
		}
		if l.Logger != loggerObtain && l.Logger != loggerRenew {
			continue
		}
		if l.Msg == msgObtained || l.Msg == msgRenewed {
			delete(out, l.Identifier)
			continue
		}
		if l.Level != levelError || l.Error == "" {
			continue
		}
		name := l.Identifier
		if name == "" {
			name = bracketedName(l.Error)
		}
		if name == "" {
			continue
		}
		if !askedCA(ca, l.Issuer, l.Error) {
			continue
		}
		status, problem, message, retry := parseObtainError(l.Error)
		// The CA's words reach the operator's terminal and setup's: an escape sequence in them is
		// made harmless here, once, for every consumer (the table, --json, setup).
		out[termtext.Clean(name)] = ObtainError{
			At: logTime(l.Ts), CA: termtext.Clean(firstNonEmpty(ca, l.Issuer)),
			Status: status, Type: termtext.Clean(problem), Message: termtext.Clean(message), RetryAfter: retry,
		}
	}
	return out
}

// urlInError finds the URLs certmagic puts in an order's error: the CA's
// new-order endpoint is the one that names the CA that answered.
var urlInError = regexp.MustCompile(`https?://[^\s"]+`)

// askedCA says an order's failure is one of the CA the Caddyfile names now. The
// journal outlives the install: a node that was rate limited by one CA and was
// then pointed at another still holds the first CA's errors, and reporting them
// would blame a CA that is no longer asked. The log names the CA by the issuer
// key (the directory's host and path, joined by "-") on the line that ends an
// order, and the retry line carries the endpoint URL in its error; a line with
// neither says nothing and is kept.
func askedCA(ca, issuerKey, errText string) bool {
	u, err := url.Parse(ca)
	if err != nil || u.Hostname() == "" {
		return true
	}
	if issuerKey != "" {
		return strings.HasPrefix(issuerKey, u.Hostname())
	}
	urls := urlInError.FindAllString(errText, -1)
	for _, raw := range urls {
		if e, err := url.Parse(raw); err == nil && e.Hostname() == u.Hostname() {
			return true
		}
	}
	return len(urls) == 0
}

// bracketedName is the name certmagic puts first in an error: "[example.com] Obtain: ...".
func bracketedName(err string) string {
	if !strings.HasPrefix(err, "[") {
		return ""
	}
	end := strings.IndexByte(err, ']')
	if end < 0 {
		return ""
	}
	return err[1:end]
}

// logTime is the time of a Caddy log line: seconds since the epoch, fractional.
func logTime(ts float64) time.Time {
	if ts <= 0 {
		return time.Time{}
	}
	sec := int64(ts)
	return time.Unix(sec, int64((ts-float64(sec))*1e9)).UTC()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
