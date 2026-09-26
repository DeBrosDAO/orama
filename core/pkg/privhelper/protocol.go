package privhelper

// SocketPath is where systemd listens for helper requests
// (orama-privhelper.socket): root:orama, mode 0660.
const SocketPath = "/run/orama-privhelper.sock"

// MaxRequestBytes bounds a request, peer lists included.
const MaxRequestBytes = 1 << 20

// Request is one command for the helper to run as root.
type Request struct {
	Argv  []string `json:"argv"`
	Input string   `json:"input,omitempty"`
}

// Response is how it went: the command's exit status and combined output.
// ExitCode ExitRefused means the helper refused the request itself.
type Response struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// ExitRefused is the exit status of a refused or undeliverable request, apart
// from any status the tools themselves use.
const ExitRefused = 126
