package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SeccompAction defines the action to take when a syscall is matched or not.
type SeccompAction string

const (
	// ActionAllow allows the syscall.
	ActionAllow SeccompAction = "SCMP_ACT_ALLOW"

	// ActionLog logs the syscall but allows it (audit mode).
	ActionLog SeccompAction = "SCMP_ACT_LOG"

	// ActionKillProcess kills the process when the syscall is made.
	ActionKillProcess SeccompAction = "SCMP_ACT_KILL_PROCESS"
)

// SeccompProfile defines a seccomp filter in the format understood by
// libseccomp / OCI runtime spec. The agent writes this to a temp file
// and applies it via the seccomp notifier or BPF loader before exec.
type SeccompProfile struct {
	DefaultAction SeccompAction    `json:"defaultAction"`
	Syscalls      []SeccompSyscall `json:"syscalls"`
}

// SeccompSyscall defines a set of syscalls and the action to take.
type SeccompSyscall struct {
	Names  []string      `json:"names"`
	Action SeccompAction `json:"action"`
}

// SeccompMode controls enforcement level.
type SeccompMode int

const (
	// SeccompEnforce kills the process on disallowed syscalls.
	SeccompEnforce SeccompMode = iota

	// SeccompAudit logs disallowed syscalls but allows them (for profiling).
	SeccompAudit
)

// baseSyscalls are syscalls every service needs for basic operation.
var baseSyscalls = []string{
	// Process lifecycle
	"exit", "exit_group", "getpid", "getppid", "gettid",
	"clone", "clone3", "fork", "vfork", "execve", "execveat",
	"wait4", "waitid",

	// Memory management
	"brk", "mmap", "munmap", "mremap", "mprotect", "madvise",
	"mlock", "munlock",

	// File operations
	"read", "write", "pread64", "pwrite64", "readv", "writev",
	"open", "openat", "close", "dup", "dup2", "dup3",
	"stat", "fstat", "lstat", "newfstatat",
	"access", "faccessat", "faccessat2",
	"lseek", "fcntl", "flock",
	"getcwd", "readlink", "readlinkat",
	"getdents64",

	// Directory operations
	"mkdir", "mkdirat", "rmdir",
	"rename", "renameat", "renameat2",
	"unlink", "unlinkat",
	"symlink", "symlinkat",
	"link", "linkat",
	"chmod", "fchmod", "fchmodat",
	"chown", "fchown", "fchownat",
	"utimensat",

	// IO multiplexing
	"epoll_create1", "epoll_ctl", "epoll_wait", "epoll_pwait", "epoll_pwait2",
	"poll", "ppoll", "select", "pselect6",
	"eventfd", "eventfd2",

	// Networking (basic)
	"socket", "connect", "accept", "accept4",
	"bind", "listen",
	"sendto", "recvfrom", "sendmsg", "recvmsg",
	"shutdown", "getsockname", "getpeername",
	"getsockopt", "setsockopt",

	// Signals
	"rt_sigaction", "rt_sigprocmask", "rt_sigreturn",
	"sigaltstack", "kill", "tgkill",

	// Time
	"clock_gettime", "clock_getres", "gettimeofday",
	"nanosleep", "clock_nanosleep",

	// Threading / synchronization
	"futex", "set_robust_list", "get_robust_list",
	"set_tid_address",

	// System info
	"uname", "getuid", "getgid", "geteuid", "getegid",
	"getgroups", "getrlimit", "setrlimit", "prlimit64",
	"sysinfo", "getrandom",

	// Pipe and IPC
	"pipe", "pipe2",
	"ioctl",

	// Misc
	"arch_prctl", "prctl", "seccomp",
	"sched_yield", "sched_getaffinity",
	"rseq",
	"close_range",
	"membarrier",
}

// ServiceSyscalls defines additional syscalls required by each service
// beyond the base set. These were determined by running services in audit
// mode (SCMP_ACT_LOG) and capturing required syscalls.
var ServiceSyscalls = map[string][]string{
	"rqlite": {
		// Raft log + SQLite WAL
		"fsync", "fdatasync", "ftruncate", "fallocate",
		"sync_file_range",
		// SQLite memory-mapped I/O
		"mincore",
		// Raft networking (TCP)
		"sendfile",
	},

	"olric": {
		// Memberlist gossip (UDP multicast + TCP)
		"sendmmsg", "recvmmsg",
		// Embedded map operations
		"fsync", "fdatasync", "ftruncate",
	},

	"ipfs": {
		// Block storage and data transfer
		"sendfile", "splice", "tee",
		// Repo management
		"fsync", "fdatasync", "ftruncate", "fallocate",
		// libp2p networking
		"sendmmsg", "recvmmsg",
	},

	"ipfs-cluster": {
		// CRDT datastore
		"fsync", "fdatasync", "ftruncate", "fallocate",
		// libp2p networking
		"sendfile",
	},

	"gateway": {
		// HTTP server
		"sendfile", "splice",
		// WebSocket
		"sendmmsg", "recvmmsg",
		// TLS
		"fsync", "fdatasync",
	},

	"coredns": {
		// DNS (UDP + TCP on port 53)
		"sendmmsg", "recvmmsg",
		// Zone file / cache
		"fsync", "fdatasync",
	},
}

// BuildProfile creates a seccomp profile for the given service.
func BuildProfile(serviceName string, mode SeccompMode) *SeccompProfile {
	defaultAction := ActionKillProcess
	if mode == SeccompAudit {
		defaultAction = ActionLog
	}

	// Combine base + service-specific syscalls
	allowed := make([]string, len(baseSyscalls))
	copy(allowed, baseSyscalls)

	if extra, ok := ServiceSyscalls[serviceName]; ok {
		allowed = append(allowed, extra...)
	}

	return &SeccompProfile{
		DefaultAction: defaultAction,
		Syscalls: []SeccompSyscall{
			{
				Names:  allowed,
				Action: ActionAllow,
			},
		},
	}
}

// WriteProfile writes a seccomp profile to a temporary file and returns the path.
// The caller is responsible for removing the file after the process starts.
func WriteProfile(serviceName string, mode SeccompMode) (string, error) {
	profile := BuildProfile(serviceName, mode)

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal seccomp profile: %w", err)
	}

	dir := "/tmp/orama-seccomp"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create seccomp dir: %w", err)
	}

	path := filepath.Join(dir, serviceName+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", fmt.Errorf("failed to write seccomp profile: %w", err)
	}

	return path, nil
}
