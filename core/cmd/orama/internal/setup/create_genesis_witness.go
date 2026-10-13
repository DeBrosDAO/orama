package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// genesisTimeKey is the one field two builds of the same genesis differ in: the
// time `oramad init` ran.
const genesisTimeKey = "genesis_time"

// sameGenesis refuses two genesis documents that differ in anything but the time
// they were made at.
func sameGenesis(a, b []byte) error {
	docA, err := decodeExact(a)
	if err != nil {
		return fmt.Errorf("the first is not JSON: %w", err)
	}
	docB, err := decodeExact(b)
	if err != nil {
		return fmt.Errorf("the second is not JSON: %w", err)
	}
	delete(docA, genesisTimeKey)
	delete(docB, genesisTimeKey)
	if reflect.DeepEqual(docA, docB) {
		return nil
	}
	return fmt.Errorf("they differ in %s: one of the two machines does not run the release it should, or is not honest", differingKeys(docA, docB))
}

// differingKeys names the top-level fields, and for app_state the modules, in
// which two documents differ.
func differingKeys(a, b map[string]any) string {
	const appState = "app_state"
	keys := differing("", a, b, appState)
	if as, ok := a[appState].(map[string]any); ok {
		if bs, ok := b[appState].(map[string]any); ok {
			keys = append(keys, differing(appState+".", as, bs, "")...)
		} else {
			keys = append(keys, appState)
		}
	} else if !reflect.DeepEqual(a[appState], b[appState]) {
		keys = append(keys, appState)
	}
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}

// differing are the keys of either map, prefixed, whose values differ, except skip.
func differing(prefix string, a, b map[string]any, skip string) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []map[string]any{a, b} {
		for k := range m {
			if k != skip && !seen[k] && !reflect.DeepEqual(a[k], b[k]) {
				keys = append(keys, prefix+k)
			}
			seen[k] = true
		}
	}
	return keys
}

// decodeExact parses a JSON object keeping numbers as written, so a difference in
// a large integer is not rounded away.
func decodeExact(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}
