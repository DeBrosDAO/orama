package shared

import "github.com/DeBrosOfficial/network/pkg/cli/printer"

// FormatBytes renders a byte count for a person.
//
// Deprecated: use printer.FormatBytes. Seven copies of this function existed
// across the CLI and they did not agree — some printed "1.0 KB" where others
// printed "1.0KB" for the same number.
func FormatBytes(bytes int64) string {
	return printer.FormatBytes(bytes)
}
