// Command fuzzcorpus writes seed corpus entries for a Go native fuzz target from real data in the
// repository (test vectors, committed transactions, network manifests), in the format `go test`
// reads from testdata/fuzz/<FuzzName>/ and runs on every plain `go test` as a regression case.
//
// Each source file, or each hex string found under a JSON key, becomes one entry. The bytes are the
// target's first fuzzed argument unless -pre arguments come before them; -post arguments follow.
// make -C chain fuzz-corpus runs it for every target that has a real-data seed, so the committed
// corpus can be rebuilt from the data it was made of.
//
//	fuzzcorpus -dir x/shielded/bundle/testdata/fuzz/FuzzBundleParse -prefix vector \
//	    -file x/shielded/orchardffi/testdata/ironwood-1-action.bundle
//	fuzzcorpus -dir app/testdata/fuzz/FuzzAnteTxDecode -prefix tx -hexjson client/tx/testdata/tx_vectors.json#tx_hex
//
// An argument is kind:value with kind one of string, bytes (the value is hex), uint8, uint16, uint32
// or uint64.
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	var files, hexJSON, pre, post list
	dir := flag.String("dir", "", "the testdata/fuzz/<FuzzName> directory to write")
	prefix := flag.String("prefix", "seed", "name of the entries, followed by their number")
	flag.Var(&files, "file", "a file whose bytes are an entry (repeatable)")
	flag.Var(&hexJSON, "hexjson", "path#key: every hex string under key in a JSON file is an entry (repeatable)")
	flag.Var(&pre, "pre", "kind:value argument before the bytes (repeatable)")
	flag.Var(&post, "post", "kind:value argument after the bytes (repeatable)")
	flag.Parse()
	if err := run(*dir, *prefix, files, hexJSON, pre, post); err != nil {
		fmt.Fprintln(os.Stderr, "fuzzcorpus:", err)
		os.Exit(1)
	}
}

func run(dir, prefix string, files, hexJSON, pre, post list) error {
	if dir == "" || (len(files) == 0 && len(hexJSON) == 0) {
		return fmt.Errorf("-dir and at least one -file or -hexjson are required")
	}
	var inputs [][]byte
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		inputs = append(inputs, raw)
	}
	for _, spec := range hexJSON {
		path, key, ok := strings.Cut(spec, "#")
		if !ok {
			return fmt.Errorf("-hexjson %q is not path#key", spec)
		}
		found, err := hexValues(path, key)
		if err != nil {
			return err
		}
		inputs = append(inputs, found...)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i, in := range inputs {
		var lines []string
		for _, a := range pre {
			l, err := argLine(a)
			if err != nil {
				return err
			}
			lines = append(lines, l)
		}
		lines = append(lines, "[]byte("+strconv.Quote(string(in))+")")
		for _, a := range post {
			l, err := argLine(a)
			if err != nil {
				return err
			}
			lines = append(lines, l)
		}
		body := "go test fuzz v1\n" + strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%02d", prefix, i)), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// hexValues returns the decoded hex strings found under key anywhere in the JSON file, in document order.
func hexValues(path, key string) ([][]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out [][]byte
	var walk func(v any) error
	walk = func(v any) error {
		switch t := v.(type) {
		case map[string]any:
			// Keys are walked in sorted order so the entries are numbered the same on every run.
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if s, ok := t[k].(string); ok && k == key {
					b, err := hex.DecodeString(s)
					if err != nil {
						return fmt.Errorf("%s: %s is not hex: %w", path, key, err)
					}
					out = append(out, b)
					continue
				}
				if err := walk(t[k]); err != nil {
					return err
				}
			}
		case []any:
			for _, e := range t {
				if err := walk(e); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(doc); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no string under %q", path, key)
	}
	return out, nil
}

// argLine renders one kind:value argument as a line of a corpus entry.
func argLine(a string) (string, error) {
	kind, value, ok := strings.Cut(a, ":")
	if !ok {
		return "", fmt.Errorf("argument %q is not kind:value", a)
	}
	switch kind {
	case "string":
		return "string(" + strconv.Quote(value) + ")", nil
	case "bytes":
		b, err := hex.DecodeString(value)
		if err != nil {
			return "", fmt.Errorf("argument %q: %w", a, err)
		}
		return "[]byte(" + strconv.Quote(string(b)) + ")", nil
	case "uint8", "uint16", "uint32", "uint64":
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return "", fmt.Errorf("argument %q: %w", a, err)
		}
		return kind + "(" + strconv.FormatUint(n, 10) + ")", nil
	}
	return "", fmt.Errorf("argument %q: unknown kind %q", a, kind)
}
