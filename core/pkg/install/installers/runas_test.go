package installers

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

func stubServiceAccount(t *testing.T, acct serviceAccount, err error) {
	t.Helper()
	orig := lookupServiceAccount
	lookupServiceAccount = func() (serviceAccount, error) { return acct, err }
	t.Cleanup(func() { lookupServiceAccount = orig })
}

// The tools run as the orama user, with its home and none of the installing
// root's environment or groups.
func TestServiceUserCommand_dropsToTheServiceUser(t *testing.T) {
	stubServiceAccount(t, serviceAccount{uid: 997, gid: 996, home: "/opt/orama"}, nil)
	t.Setenv("ROOT_ONLY_SECRET", "x")

	cmd, err := serviceUserCommand([]string{"IPFS_PATH=/opt/orama/.orama/data/ipfs/repo"}, "/usr/local/bin/ipfs", "init")
	if err != nil {
		t.Fatal(err)
	}
	cred := cmd.SysProcAttr.Credential
	if cred == nil || cred.Uid != 997 || cred.Gid != 996 {
		t.Fatalf("credential = %+v, want uid 997 gid 996", cred)
	}
	if cred.NoSetGroups || len(cred.Groups) != 0 {
		t.Errorf("supplementary groups are not cleared: %+v", cred)
	}
	env := strings.Join(cmd.Env, "\n")
	for _, want := range []string{"HOME=/opt/orama", "IPFS_PATH=/opt/orama/.orama/data/ipfs/repo", serviceUserPath} {
		if !strings.Contains(env, want) {
			t.Errorf("environment lacks %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "ROOT_ONLY_SECRET") {
		t.Error("the installing process's environment was handed down")
	}
	if cmd.Dir != "/opt/orama" {
		t.Errorf("working directory = %q", cmd.Dir)
	}
}

// Without the orama user nothing runs — never as root instead.
func TestServiceUserCommand_noServiceUserIsAnError(t *testing.T) {
	stubServiceAccount(t, serviceAccount{}, errors.New("user: unknown user orama"))
	if _, err := serviceUserCommand(nil, "ipfs", "init"); err == nil {
		t.Fatal("a command was built without the service user")
	}
	if err := runAsServiceUser(nil, "ipfs", "init"); err == nil {
		t.Fatal("ran without the service user")
	}
}

// The directory handed over is chowned through rootfs, so a symlink planted
// in its place is refused, not followed.
func TestGiveToServiceUser_refusesASymlink(t *testing.T) {
	stubServiceAccount(t, serviceAccount{uid: uint32(os.Getuid()), gid: uint32(os.Getgid())}, nil)
	anchor := t.TempDir()
	repo := filepath.Join(anchor, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := giveToServiceUser(rootfs.At(anchor), repo); err != nil {
		t.Fatalf("a plain directory: %v", err)
	}

	link := filepath.Join(anchor, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if err := giveToServiceUser(rootfs.At(anchor), link); err == nil {
		t.Fatal("chowned through a symlink")
	}
}

// The repo tools run through runAsServiceUser. An exec.Command of the resolved
// ipfs or ipfs-cluster-service binary in the installers runs it as root inside
// the orama user's tree.
func TestRepoTools_neverRunAsRoot(t *testing.T) {
	fset := token.NewFileSet()
	for _, file := range []string{"ipfs.go", "ipfs_cluster.go"} {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Command" {
				return true
			}
			if id, ok := call.Args[0].(*ast.Ident); ok && (id.Name == "ipfsBinary" || id.Name == "clusterBinary") {
				t.Errorf("%s: exec.Command(%s, ...) runs as root; use runAsServiceUser", fset.Position(call.Pos()), id.Name)
			}
			return true
		})
	}
}
