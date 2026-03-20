package sandbox

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// fanoutArchive uploads a binary archive to the first server, then fans out
// server-to-server in parallel to all remaining servers. This is much faster
// than uploading from the local machine to each node individually.
// After distribution, the archive is extracted on all nodes.
func fanoutArchive(servers []ServerState, sshKeyPath, archivePath string) error {
	remotePath := "/tmp/" + filepath.Base(archivePath)
	extractCmd := fmt.Sprintf("mkdir -p /opt/orama && tar xzf %s -C /opt/orama && rm -f %s",
		remotePath, remotePath)

	// Step 1: Upload from local machine to first node
	first := servers[0]
	firstNode := inspector.Node{User: "root", Host: first.IP, SSHKey: sshKeyPath}

	fmt.Printf("  Uploading to %s...\n", first.Name)
	if err := remotessh.UploadFile(firstNode, archivePath, remotePath, remotessh.WithNoHostKeyCheck()); err != nil {
		return fmt.Errorf("upload to %s: %w", first.Name, err)
	}

	// Step 2: Fan out from first node to remaining nodes in parallel (server-to-server)
	if len(servers) > 1 {
		fmt.Printf("  Fanning out from %s to %d nodes...\n", first.Name, len(servers)-1)

		// Temporarily upload SSH key for server-to-server SCP
		remoteKeyPath := "/tmp/.sandbox_key"
		if err := remotessh.UploadFile(firstNode, sshKeyPath, remoteKeyPath, remotessh.WithNoHostKeyCheck()); err != nil {
			return fmt.Errorf("upload SSH key to %s: %w", first.Name, err)
		}
		defer remotessh.RunSSHStreaming(firstNode, fmt.Sprintf("rm -f %s", remoteKeyPath), remotessh.WithNoHostKeyCheck())

		if err := remotessh.RunSSHStreaming(firstNode, fmt.Sprintf("chmod 600 %s", remoteKeyPath), remotessh.WithNoHostKeyCheck()); err != nil {
			return fmt.Errorf("chmod SSH key on %s: %w", first.Name, err)
		}

		var wg sync.WaitGroup
		errs := make([]error, len(servers))

		for i := 1; i < len(servers); i++ {
			wg.Add(1)
			go func(idx int, srv ServerState) {
				defer wg.Done()
				// SCP from first node to target
				scpCmd := fmt.Sprintf("scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i %s %s root@%s:%s",
					remoteKeyPath, remotePath, srv.IP, remotePath)
				if err := remotessh.RunSSHStreaming(firstNode, scpCmd, remotessh.WithNoHostKeyCheck()); err != nil {
					errs[idx] = fmt.Errorf("fanout to %s: %w", srv.Name, err)
					return
				}
				// Extract on target
				targetNode := inspector.Node{User: "root", Host: srv.IP, SSHKey: sshKeyPath}
				if err := remotessh.RunSSHStreaming(targetNode, extractCmd, remotessh.WithNoHostKeyCheck()); err != nil {
					errs[idx] = fmt.Errorf("extract on %s: %w", srv.Name, err)
					return
				}
				fmt.Printf("  Distributed to %s\n", srv.Name)
			}(i, servers[i])
		}
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				return err
			}
		}
	}

	// Step 3: Extract on first node
	fmt.Printf("  Extracting on %s...\n", first.Name)
	if err := remotessh.RunSSHStreaming(firstNode, extractCmd, remotessh.WithNoHostKeyCheck()); err != nil {
		return fmt.Errorf("extract on %s: %w", first.Name, err)
	}

	return nil
}
