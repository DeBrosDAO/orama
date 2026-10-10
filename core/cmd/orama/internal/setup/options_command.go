package setup

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// safeArg is an argument a shell takes as it is.
var safeArg = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellArg(s string) string {
	if safeArg.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// CommandLine is the `orama setup` command that runs these options again. It is
// what a stopped run tells the operator to type: every choice that changes what
// the run does is in it (the declined validator, the exit relay's consent, the
// storage), and the host-key fingerprints an unattended run needs. A password
// typed into the wizard is not, because it is never kept; the machines already
// have the RootWallet's key by then.
func (o Options) CommandLine() string {
	parts := []string{"orama", "setup"}
	flag := func(name, value string) {
		if value != "" {
			parts = append(parts, "--"+name, shellArg(value))
		}
	}
	on := func(name string, set bool) {
		if set {
			parts = append(parts, "--"+name)
		}
	}
	flag("network", o.Network)
	flag("env", o.Env)
	flag("name", o.Name)
	for _, ip := range o.IPs {
		if user, ok := o.Users[ip]; ok {
			flag("ip", user+"@"+ip)
		} else {
			flag("ip", ip)
		}
	}
	for _, ip := range sortedKeys(o.HostKeys) {
		if ip == "" {
			flag("host-key", o.HostKeys[ip])
		} else {
			flag("host-key", ip+"="+o.HostKeys[ip])
		}
	}
	if o.User != DefaultSSHUser {
		flag("user", o.User)
	}
	on("password", o.UsePassword && o.Password == "")
	flag("bootstrap-key", o.BootstrapKey)
	flag("domain", o.Domain)
	flag("acme-ca", o.ACMECA)
	on("cluster-only", o.ClusterOnly)
	if !o.ClusterOnly && o.StorageGB != DefaultStorageGB && o.StorageGB != 0 {
		flag("storage-gb", strconv.FormatUint(o.StorageGB, 10))
	}
	on("exit", o.Exit)
	flag("contact", o.Contact)
	if o.ASNSet {
		flag("asn", fmt.Sprint(o.ASN))
	}
	flag("tor-network", o.TorNetwork)
	on("no-relay", o.NoRelay)
	on("upload-release", o.UploadRelease)
	on("no-validator", o.NoValidator)
	on("allow-quorum-loss", o.AllowQuorumLoss)
	parts = append(parts, o.Create.commandLine()...)
	parts = append(parts, "--yes")
	return strings.Join(parts, " ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
