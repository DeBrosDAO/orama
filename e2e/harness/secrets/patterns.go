package secrets

import "regexp"

// pattern is a shape of secret recognised without knowing its value; repl is
// the replacement (regexp.Expand syntax) that keeps the context around the mask.
type pattern struct {
	re   *regexp.Regexp
	repl string
}

// Replacements.
const (
	maskAll    = Mask
	keepFirst  = "${1}" + Mask
	keepTwo    = "${1}${2}" + Mask
	keepURLTop = "${1}" + Mask + "@"
)

// secretWord is what makes a key name a credential's: accessToken,
// refresh_token, CLIENT_SECRET, db_password, passwd, apiKey, x-api-key,
// private_key, PrivateKey, PresharedKey, mnemonic, psk, swarm_key.
// passphrase, encryption_key, privkey, signing_key, hmac_key, seed.
const secretWord = `(?:token|secret|passw(?:or)?d|passphrase|api[_-]?key|private[_-]?key|priv[_-]?key|preshared[_-]?key|` +
	`encryption[_-]?key|signing[_-]?key|hmac[_-]?key|mnemonic|seed|psk|swarm[_-]?key)`

// q is an optional quote, itself optionally backslash-escaped: the same
// patterns then match JSON and JSON embedded in a JSON string (go test -json
// output, a logged request body).
const q = `(?:\\*")?`

// headerSep separates a header name from its value: "Name: v", "name=v",
// "\"name\":\"v", and a printed http.Header's "Name:[v]" (the bracket only
// right after the colon, so a masked "Name: [REDACTED]" is not re-read).
const headerSep = q + `\s*[:=](?:\[|\s*)` + q

// headerValue is a header value up to the end of the line, a quote or the
// closing bracket of a printed http.Header. It cannot start with "[" so an
// already masked value is left alone: redaction is idempotent.
const headerValue = `[^\s"\\\[\]][^\r\n"\\\]]*`

var patterns = []pattern{
	// PEM private keys (SSH, TLS, anything the collector reads off a node).
	{re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), repl: maskAll},
	// IPFS swarm key file body: the 64 hex digits after /base16/.
	{re: regexp.MustCompile(`(/key/swarm/psk/1\.0\.0/(?:\s|\\n|\\r)*/base16/(?:\s|\\n|\\r)*)[0-9a-fA-F]{64}`), repl: keepFirst},
	// Authorization-type headers: the whole value, scheme included (Bearer,
	// Basic, Token, Digest, or none), to the end of the value.
	// X-...-Token, X-...-Key and X-...-Auth headers (X-Api-Key, X-Auth-Token,
	// X-Orama-Key) the same way.
	{re: regexp.MustCompile(`(?i)((?:proxy-)?authorization|x-[a-z0-9-]*(?:token|key|auth)[a-z0-9-]*)(` + headerSep + `)` + headerValue), repl: keepTwo},
	// Cookies, sent and set.
	{re: regexp.MustCompile(`(?i)((?:set-)?cookie` + headerSep + `)` + headerValue), repl: keepFirst},
	// JSON members whose key names a credential, in plain or (once or
	// twice) escaped JSON.
	{re: regexp.MustCompile(`(?i)(` + q + `[A-Za-z0-9_.-]*` + secretWord + `[A-Za-z0-9_.-]*` + q + `\s*:\s*\\*")((?:[^"\\]|\\+[^"\\])*)`), repl: keepFirst},
	// The same members with a number or an array of numbers (a key as
	// bytes, a numeric token).
	{re: regexp.MustCompile(`(?i)(` + q + `[A-Za-z0-9_.-]*` + secretWord + `[A-Za-z0-9_.-]*` + q + `\s*:\s*)(\[[0-9,\s]*[0-9][0-9,\s]*\]|-?[0-9]{4,})`), repl: keepFirst},
	// JWK private member "d" and device codes: the key quoted, so "id",
	// "node_id" and "cid" are never taken for it.
	{re: regexp.MustCompile(`(\\*"(?:d|device_code)\\*"\s*:\s*\\*")((?:[^"\\]|\\+[^"\\])*)`), repl: keepFirst},
	// KEY=value (environment, .env, query strings, flags, WireGuard's
	// PrivateKey = ...), quoted or not.
	{re: regexp.MustCompile(`(?i)(\b[A-Za-z0-9_-]*` + secretWord + `[A-Za-z0-9_-]*\s*=\s*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s"'&,;]+)`), repl: keepFirst},
	// YAML: key: value at the start of a line (a key never contains a dot,
	// so "token_test.go:12: ..." test output is not a key).
	{re: regexp.MustCompile(`(?im)^(\s*-?\s*[A-Za-z0-9_-]*` + secretWord + `[A-Za-z0-9_-]*\s*:[ \t]+)(\S[^\r\n]*)`), repl: keepFirst},
	// A flag naming a credential with its value after a space:
	// --password hunter2, -token abc (the "=" form is KEY=value above).
	{re: regexp.MustCompile(`(?i)((?:^|\s)--?[A-Za-z0-9_-]*` + secretWord + `[A-Za-z0-9_-]*[ \t]+)([^\s\-\[][^\s]*)`), repl: keepFirst},
	// curl-style user:password: -u user:pw, --user user:pw.
	{re: regexp.MustCompile(`((?:^|\s)(?:-u|--user)[ \t]+[^\s:\[]+:)([^\s\[][^\s]*)`), repl: keepFirst},
	// The cluster secret and other node secrets named mid-line:
	// "cluster secret: x", "cluster_secret: x", "rqlite password: x".
	{re: regexp.MustCompile(`(?i)(\b(?:cluster|rqlite|olric|turn)[ _-](?:secret|password|key)\s*[:=][ \t]*)([^\s\[][^\s,;]*)`), repl: keepFirst},
	// Signature and key query parameters: ?key=, &sig=, &signature=.
	{re: regexp.MustCompile(`(?i)([?&](?:key|sig|signature)=)([^&\s"'\[#]+)`), repl: keepFirst},
	// A BIP-39 phrase of 12 to 24 words after a mnemonic, seed or phrase
	// label, on the label's line or the next.
	{re: regexp.MustCompile(`(?i)((?:mnemonic|seed|phrase)[^\n]{0,24}?[:=]?[ \t]*(?:\r?\n[ \t]*)?)((?:[a-z]{3,8}[ \t]+){11,23}[a-z]{3,8})\b`), repl: keepFirst},
	// 32-byte hex keys: 0x followed by 64 hex digits (an EVM private key).
	{re: regexp.MustCompile(`\b0x[0-9a-fA-F]{64}\b`), repl: maskAll},
	// Credentials in URLs: scheme://user:password@host.
	{re: regexp.MustCompile(`(\b[A-Za-z][A-Za-z0-9+.-]*://)[^/\s:@"'\\]+:[^/\s@"'\\]+@`), repl: keepURLTop},
	// JWTs anywhere (three base64url segments, header starting {"...).
	{re: regexp.MustCompile(`eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}`), repl: maskAll},
	// Orama API keys: orama_<type>_<payload>_<checksum>, base62.
	{re: regexp.MustCompile(`orama_[A-Za-z0-9]+_[A-Za-z0-9]+_[A-Za-z0-9]+`), repl: maskAll},
}

func redactPatterns(s string) string {
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}
