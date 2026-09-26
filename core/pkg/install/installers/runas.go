package installers

import (
	"fmt"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// Install and upgrade run as root, and the IPFS repository and the IPFS
// Cluster directory belong to the orama user, who could plant a symlink in
// either. `ipfs init`, `ipfs config` and `ipfs-cluster-service init` resolve
// paths there themselves — outside pkg/rootfs — so as root they would follow
// it. They run as the orama user instead, the account the daemons run as:
// whatever they can reach, the daemon could already.

// serviceUserName is the account every Orama service runs as.
const serviceUserName = "orama"

// serviceUserPath is the PATH a command run as the service user gets; the
// installing root's environment is not handed down.
const serviceUserPath = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// serviceAccount is the service user's identity.
type serviceAccount struct {
	uid, gid uint32
	home     string
}

// lookupServiceAccount finds the service user. A variable so tests can stand
// one in; nothing else assigns it.
var lookupServiceAccount = func() (serviceAccount, error) {
	u, err := user.Lookup(serviceUserName)
	if err != nil {
		return serviceAccount{}, fmt.Errorf("look up the %s user (Phase 2 creates it): %w", serviceUserName, err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return serviceAccount{}, fmt.Errorf("the %s user's uid %q: %w", serviceUserName, u.Uid, err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return serviceAccount{}, fmt.Errorf("the %s user's gid %q: %w", serviceUserName, u.Gid, err)
	}
	return serviceAccount{uid: uint32(uid), gid: uint32(gid), home: u.HomeDir}, nil
}

// serviceUserCommand is name args, to run as the service user with only
// PATH, HOME and env in its environment. Supplementary groups are cleared.
func serviceUserCommand(env []string, name string, args ...string) (*exec.Cmd, error) {
	acct, err := lookupServiceAccount()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(name, args...)
	// Setsid puts the service user's process in its own session. Without it
	// the child stays in the installing root's session: it keeps that
	// controlling terminal and receives the installer's signals.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: acct.uid, Gid: acct.gid, Groups: []uint32{}},
		Setsid:     true,
	}
	cmd.Env = append([]string{serviceUserPath, "HOME=" + acct.home}, env...)
	cmd.Dir = acct.home
	return cmd, nil
}

// giveToServiceUser makes dir, which root may just have created, the service
// user's, so a tool run as that user can write in it. rootfs refuses a
// symlink in its place.
func giveToServiceUser(root rootfs.Root, dir string) error {
	acct, err := lookupServiceAccount()
	if err != nil {
		return err
	}
	if err := root.Chown(dir, int(acct.uid), int(acct.gid)); err != nil {
		return fmt.Errorf("hand %s to the %s user: %w", dir, serviceUserName, err)
	}
	return nil
}

// runAsServiceUser runs name args as the service user and folds its output
// into the error when it fails.
func runAsServiceUser(env []string, name string, args ...string) error {
	cmd, err := serviceUserCommand(env, name, args...)
	if err != nil {
		return err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %v as %s: %w\n%s", name, args, serviceUserName, err, string(out))
	}
	return nil
}
