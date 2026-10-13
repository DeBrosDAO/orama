package shared

import (
	"encoding/json"
	"fmt"
	"os"
)

// PrintJSON pretty-prints data as indented JSON to stdout.
func PrintJSON(data interface{}) {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal JSON: %v\n", err)
		return
	}
	fmt.Println(string(jsonData))
}
