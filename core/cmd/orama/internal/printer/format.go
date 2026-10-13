package printer

import "fmt"

// byteUnit is the step between size units. Sizes here describe files and
// archives, which every other tool on the machine reports in powers of two.
const byteUnit = 1024

// FormatBytes renders a byte count for a person.
//
// Seven copies of this existed across the CLI, and they did not agree: some
// printed "1.0 KB" where others printed "1.0 KiB" for the same number, and one
// returned "1024 B" where another returned "1.0 KB".
func FormatBytes(n int64) string {
	if n < byteUnit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(byteUnit), 0
	for rest := n / byteUnit; rest >= byteUnit; rest /= byteUnit {
		div *= byteUnit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
