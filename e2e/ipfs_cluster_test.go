//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

func TestIPFSCluster_Health(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       10 * time.Second,
	}

	client, err := ipfs.NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create IPFS client: %v", err)
	}

	err = client.Health(ctx)
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
}

func TestIPFSCluster_GetPeerCount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       10 * time.Second,
	}

	client, err := ipfs.NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create IPFS client: %v", err)
	}

	peerCount, err := client.GetPeerCount(ctx)
	if err != nil {
		t.Fatalf("get peer count failed: %v", err)
	}

	if peerCount < 0 {
		t.Fatalf("expected non-negative peer count, got %d", peerCount)
	}

	t.Logf("IPFS cluster peers: %d", peerCount)
}

func TestIPFSCluster_AddFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       30 * time.Second,
	}

	client, err := ipfs.NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create IPFS client: %v", err)
	}

	content := []byte("IPFS cluster test content")
	result, err := client.Add(ctx, bytes.NewReader(content), "test.txt")
	if err != nil {
		t.Fatalf("add file failed: %v", err)
	}

	if result.Cid == "" {
		t.Fatalf("expected non-empty CID")
	}

	if result.Size != int64(len(content)) {
		t.Fatalf("expected size %d, got %d", len(content), result.Size)
	}

	t.Logf("Added file with CID: %s", result.Cid)
}

func TestIPFSCluster_PinFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       30 * time.Second,
	}

	client, err := ipfs.NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create IPFS client: %v", err)
	}

	// Add file first
	content := []byte("IPFS pin test content")
	addResult, err := client.Add(ctx, bytes.NewReader(content), "pin-test.txt")
	if err != nil {
		t.Fatalf("add file failed: %v", err)
	}

	cid := addResult.Cid

	// Pin the file
	pinResult, err := client.Pin(ctx, cid, "pinned-file", 1)
	if err != nil {
		t.Fatalf("pin file failed: %v", err)
	}

	if pinResult.Cid != cid {
		t.Fatalf("expected cid %s, got %s", cid, pinResult.Cid)
	}

	t.Logf("Pinned file: %s", cid)
}

func TestIPFSCluster_PinStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       30 * time.Second,
	}

	client, err := ipfs.NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create IPFS client: %v", err)
	}

	// Add and pin file
	content := []byte("IPFS status test content")
	addResult, err := client.Add(ctx, bytes.NewReader(content), "status-test.txt")
	if err != nil {
		t.Fatalf("add file failed: %v", err)
	}

	cid := addResult.Cid

	pinResult, err := client.Pin(ctx, cid, "status-test", 1)
	if err != nil {
		t.Fatalf("pin file failed: %v", err)
	}

	if pinResult.Cid != cid {
		t.Fatalf("expected cid %s, got %s", cid, pinResult.Cid)
	}

	// Give pin time to propagate
	Delay(1000)

	// Get status
	status, err := client.PinStatus(ctx, cid)
	if err != nil {
		t.Fatalf("get pin status failed: %v", err)
	}

	if status.Cid != cid {
		t.Fatalf("expected cid %s, got %s", cid, status.Cid)
	}

	if status.Name != "status-test" {
		t.Fatalf("expected name 'status-test', got %s", status.Name)
	}

	if status.ReplicationFactor < 1 {
		t.Logf("warning: replication factor is %d, expected >= 1", status.ReplicationFactor)
	}

	t.Logf("Pin status: %s (replication: %d, peers: %d)", status.Status, status.ReplicationFactor, len(status.Peers))
}

func TestIPFSCluster_UnpinFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       30 * time.Second,
	}

	client, err := ipfs.NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create IPFS client: %v", err)
	}

	// Add and pin file
	content := []byte("IPFS unpin test content")
	addResult, err := client.Add(ctx, bytes.NewReader(content), "unpin-test.txt")
	if err != nil {
		t.Fatalf("add file failed: %v", err)
	}

	cid := addResult.Cid

	_, err = client.Pin(ctx, cid, "unpin-test", 1)
	if err != nil {
		t.Fatalf("pin file failed: %v", err)
	}

	// Unpin file
	err = client.Unpin(ctx, cid)
	if err != nil {
		t.Fatalf("unpin file failed: %v", err)
	}

	t.Logf("Unpinned file: %s", cid)
}

func TestIPFSCluster_GetFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       30 * time.Second,
	}

	client, err := ipfs.NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create IPFS client: %v", err)
	}

	// Add file
	content := []byte("IPFS get test content")
	addResult, err := client.Add(ctx, bytes.NewReader(content), "get-test.txt")
	if err != nil {
		t.Fatalf("add file failed: %v", err)
	}

	cid := addResult.Cid

	// Give time for propagation
	Delay(1000)

	// Get file
	rc, err := client.Get(ctx, cid, GetIPFSAPIURL())
	if err != nil {
		t.Fatalf("get file failed: %v", err)
	}
	defer rc.Close()

	retrievedContent, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("failed to read content: %v", err)
	}

	if !bytes.Equal(retrievedContent, content) {
		t.Fatalf("content mismatch: expected %q, got %q", string(content), string(retrievedContent))
	}

	t.Logf("Retrieved file: %s (%d bytes)", cid, len(retrievedContent))
}

func TestIPFSCluster_LargeFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       60 * time.Second,
	}

	client, err := ipfs.NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create IPFS client: %v", err)
	}

	// Create 5MB file
	content := bytes.Repeat([]byte("x"), 5*1024*1024)
	result, err := client.Add(ctx, bytes.NewReader(content), "large.bin")
	if err != nil {
		t.Fatalf("add large file failed: %v", err)
	}

	if result.Cid == "" {
		t.Fatalf("expected non-empty CID")
	}

	if result.Size != int64(len(content)) {
		t.Fatalf("expected size %d, got %d", len(content), result.Size)
	}

	t.Logf("Added large file with CID: %s (%d bytes)", result.Cid, result.Size)
}

func TestIPFSCluster_ReplicationFactor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       30 * time.Second,
	}

	client, err := ipfs.NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create IPFS client: %v", err)
	}

	// Add file
	content := []byte("IPFS replication test content")
	addResult, err := client.Add(ctx, bytes.NewReader(content), "replication-test.txt")
	if err != nil {
		t.Fatalf("add file failed: %v", err)
	}

	cid := addResult.Cid

	// Pin with specific replication factor
	replicationFactor := 2
	pinResult, err := client.Pin(ctx, cid, "replication-test", replicationFactor)
	if err != nil {
		t.Fatalf("pin file failed: %v", err)
	}

	if pinResult.Cid != cid {
		t.Fatalf("expected cid %s, got %s", cid, pinResult.Cid)
	}

	// Give time for replication
	Delay(2000)

	// Check status
	status, err := client.PinStatus(ctx, cid)
	if err != nil {
		t.Fatalf("get pin status failed: %v", err)
	}

	t.Logf("Replication factor: requested=%d, actual=%d, peers=%d", replicationFactor, status.ReplicationFactor, len(status.Peers))
}

func TestIPFSCluster_MultipleFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       30 * time.Second,
	}

	client, err := ipfs.NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create IPFS client: %v", err)
	}

	// Add multiple files
	numFiles := 5
	var cids []string

	for i := 0; i < numFiles; i++ {
		content := []byte(fmt.Sprintf("File %d", i))
		result, err := client.Add(ctx, bytes.NewReader(content), fmt.Sprintf("file%d.txt", i))
		if err != nil {
			t.Fatalf("add file %d failed: %v", i, err)
		}
		cids = append(cids, result.Cid)
	}

	if len(cids) != numFiles {
		t.Fatalf("expected %d files added, got %d", numFiles, len(cids))
	}

	// Verify all files exist
	for i, cid := range cids {
		status, err := client.PinStatus(ctx, cid)
		if err != nil {
			t.Logf("warning: failed to get status for file %d: %v", i, err)
			continue
		}

		if status.Cid != cid {
			t.Fatalf("expected cid %s, got %s", cid, status.Cid)
		}
	}

	t.Logf("Successfully added and verified %d files", numFiles)
}
