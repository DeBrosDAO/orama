// Command orama-privhelper runs one allow-listed root command on behalf of the
// unprivileged orama user. See pkg/privhelper for what it allows and why.
package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// toolPaths are where each tool lives on the supported releases. The helper
// never consults PATH: it runs as root on behalf of another user.
var toolPaths = map[string][]string{
	privhelper.ToolSystemctl: {"/usr/bin/systemctl", "/bin/systemctl"},
	privhelper.ToolUFW:       {"/usr/sbin/ufw", "/sbin/ufw"},
}

// exitRefused distinguishes a refusal from the tool's own failures.
const exitRefused = 126

func main() {
	inv, err := privhelper.Validate(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "orama-privhelper: refused: %v\n", err)
		os.Exit(exitRefused)
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "orama-privhelper: must run as root (through sudo)")
		os.Exit(exitRefused)
	}
	bin := ""
	for _, p := range toolPaths[inv.Tool] {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			bin = p
			break
		}
	}
	if bin == "" {
		fmt.Fprintf(os.Stderr, "orama-privhelper: %s not found in %v\n", inv.Tool, toolPaths[inv.Tool])
		os.Exit(exitRefused)
	}
	env := []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	err = syscall.Exec(bin, append([]string{inv.Tool}, inv.Args...), env)
	fmt.Fprintf(os.Stderr, "orama-privhelper: exec %s: %v\n", bin, err)
	os.Exit(exitRefused)
}
