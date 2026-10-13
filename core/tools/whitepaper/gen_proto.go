package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// protoRoot holds the chain's module protos, repository-relative.
const protoRoot = "chain/proto/orama"

var (
	rpcRe     = regexp.MustCompile(`^\s*rpc\s+(\w+)\s*\(\s*(\w+)\s*\)\s*returns`)
	httpGetRe = regexp.MustCompile(`\.(get|post)\s*=\s*"([^"]+)"`)
	messageRe = regexp.MustCompile(`^\s*message\s+(\w+)\s*\{`)
	fieldRe   = regexp.MustCompile(`^\s*(repeated\s+|optional\s+)?([\w.]+)\s+(\w+)\s*=\s*\d+`)
	serviceRe = regexp.MustCompile(`^\s*service\s+(\w+)\s*\{`)
)

type protoRPC struct {
	service, name, request, comment, path string
}

type protoMessage struct {
	comment string
	fields  []string
}

// protoFile is what Appendix E needs from one .proto file.
type protoFile struct {
	rpcs     []protoRPC
	messages map[string]protoMessage
}

// genChainMessages renders Appendix E: every module's Msg and Query service.
func genChainMessages(b *Book) ([]byte, error) {
	mods, err := os.ReadDir(filepath.Join(b.Root, protoRoot))
	if err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", protoRoot, err)
	}
	var sb strings.Builder
	sb.WriteString(generatedHeader(titleOf(b, "appendices/e-chain-messages-and-queries.md"), "`"+protoRoot+"/*/v1/{tx,query}.proto`"))
	sb.WriteString("Every transaction message and query of the Orama chain modules, with its request fields and its proto comment. Volume II explains the modules.\n")
	for _, m := range mods {
		if !m.IsDir() {
			continue
		}
		if err := writeModule(&sb, b, m.Name()); err != nil {
			return nil, err
		}
	}
	return []byte(sb.String()), nil
}

func writeModule(sb *strings.Builder, b *Book, mod string) error {
	dir := filepath.Join(b.Root, protoRoot, mod, "v1")
	fmt.Fprintf(sb, "\n## x/%s\n", mod)
	for _, kind := range []struct{ file, title, svc string }{
		{"tx.proto", "Messages", "Msg"},
		{"query.proto", "Queries", "Query"},
	} {
		path := filepath.Join(dir, kind.file)
		if !fileExists(path) {
			fmt.Fprintf(sb, "\n### %s\n\n`x/%s` has no %s service.\n", kind.title, mod, kind.svc)
			continue
		}
		pf, err := parseProto(path)
		if err != nil {
			return err
		}
		anchor := filepath.ToSlash(filepath.Join(protoRoot, mod, "v1", kind.file))
		fmt.Fprintf(sb, "\n### %s\n\nSource: `%s`\n\n| %s | Request fields | Description |\n|---|---|---|\n", kind.title, anchor, kind.svc)
		for _, r := range pf.rpcs {
			if r.service != kind.svc {
				continue
			}
			msg := pf.messages[r.request]
			desc := firstNonEmpty(r.comment, msg.comment)
			name := "`" + r.name + "`"
			if r.path != "" {
				name += " `" + r.path + "`"
			}
			fmt.Fprintf(sb, "| %s | %s | %s |\n", name, codeList(msg.fields), tableCell(desc))
		}
	}
	return nil
}

// parseProto reads services, rpcs and top-level messages with their leading
// comments. It is a line scanner for the repository's proto style, not a
// general proto parser.
func parseProto(path string) (*protoFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	pf := &protoFile{messages: map[string]protoMessage{}}
	var comment []string
	service, message := "", ""
	depth := 0
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") {
			comment = append(comment, strings.TrimSpace(strings.TrimPrefix(t, "//")))
			continue
		}
		pending := strings.Join(comment, " ")
		comment = nil
		switch {
		case depth == 0 && serviceRe.MatchString(t):
			service = serviceRe.FindStringSubmatch(t)[1]
		case depth == 0 && messageRe.MatchString(t):
			message = messageRe.FindStringSubmatch(t)[1]
			pf.messages[message] = protoMessage{comment: pending}
		case service != "" && rpcRe.MatchString(t):
			m := rpcRe.FindStringSubmatch(t)
			pf.rpcs = append(pf.rpcs, protoRPC{service: service, name: m[1], request: m[2], comment: pending})
		case service != "" && httpGetRe.MatchString(t) && len(pf.rpcs) > 0:
			pf.rpcs[len(pf.rpcs)-1].path = httpGetRe.FindStringSubmatch(t)[2]
		case message != "" && depth >= 1 && fieldRe.MatchString(t):
			m := fieldRe.FindStringSubmatch(t)
			msg := pf.messages[message]
			msg.fields = append(msg.fields, m[3]+" "+strings.TrimSpace(m[1]+m[2]))
			pf.messages[message] = msg
		}
		depth += strings.Count(t, "{") - strings.Count(t, "}")
		if depth == 0 {
			service, message = "", ""
		}
	}
	sort.SliceStable(pf.rpcs, func(i, j int) bool { return pf.rpcs[i].service < pf.rpcs[j].service })
	return pf, nil
}

func codeList(fields []string) string {
	if len(fields) == 0 {
		return "none"
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = "`" + f + "`"
	}
	return strings.Join(parts, ", ")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
