package development

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func (pm *ProcessManager) printStartupSummary(topology *Topology) {
	fmt.Fprintf(pm.logWriter, "\n✅ Development environment ready!\n")
	fmt.Fprintf(pm.logWriter, "═══════════════════════════════════════\n\n")

	fmt.Fprintf(pm.logWriter, "📡 Access your nodes via unified gateway ports:\n\n")
	for _, node := range topology.Nodes {
		fmt.Fprintf(pm.logWriter, "  %s:\n", node.Name)
		fmt.Fprintf(pm.logWriter, "    curl http://localhost:%d/health\n", node.UnifiedGatewayPort)
		fmt.Fprintf(pm.logWriter, "    curl http://localhost:%d/rqlite/http/db/execute\n", node.UnifiedGatewayPort)
		fmt.Fprintf(pm.logWriter, "    curl http://localhost:%d/cluster/health\n\n", node.UnifiedGatewayPort)
	}

	fmt.Fprintf(pm.logWriter, "🌐 Main Gateway:\n")
	fmt.Fprintf(pm.logWriter, "  curl http://localhost:%d/v1/status\n\n", topology.GatewayPort)

	fmt.Fprintf(pm.logWriter, "📊 Other Services:\n")
	fmt.Fprintf(pm.logWriter, "  Olric:        http://localhost:%d\n", topology.OlricHTTPPort)
	fmt.Fprintf(pm.logWriter, "  Anon SOCKS:   127.0.0.1:%d\n", topology.AnonSOCKSPort)
	fmt.Fprintf(pm.logWriter, "  Rqlite MCP:   http://localhost:%d/sse\n\n", topology.MCPPort)

	fmt.Fprintf(pm.logWriter, "📝 Useful Commands:\n")
	fmt.Fprintf(pm.logWriter, "  ./bin/orama dev status  - Check service status\n")
	fmt.Fprintf(pm.logWriter, "  ./bin/orama dev logs node-1  - View logs\n")
	fmt.Fprintf(pm.logWriter, "  ./bin/orama dev down    - Stop all services\n\n")

	fmt.Fprintf(pm.logWriter, "📂 Logs: %s/logs\n", pm.oramaDir)
	fmt.Fprintf(pm.logWriter, "⚙️  Config: %s\n\n", pm.oramaDir)
}

func (pm *ProcessManager) stopProcess(name string) error {
	pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("%s.pid", name))
	pidBytes, err := os.ReadFile(pidPath)
	if err != nil {
		return nil
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		os.Remove(pidPath)
		return nil
	}

	if !checkProcessRunning(pid) {
		os.Remove(pidPath)
		fmt.Fprintf(pm.logWriter, "✓ %s (not running)\n", name)
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidPath)
		return nil
	}

	proc.Signal(os.Interrupt)

	gracefulShutdown := false
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if !checkProcessRunning(pid) {
			gracefulShutdown = true
			break
		}
	}

	if !gracefulShutdown && checkProcessRunning(pid) {
		proc.Signal(os.Kill)
		time.Sleep(200 * time.Millisecond)

		if runtime.GOOS != "windows" {
			exec.Command("pkill", "-9", "-P", fmt.Sprintf("%d", pid)).Run()
		}

		if checkProcessRunning(pid) {
			exec.Command("kill", "-9", fmt.Sprintf("%d", pid)).Run()
			time.Sleep(100 * time.Millisecond)
		}
	}

	os.Remove(pidPath)

	if gracefulShutdown {
		fmt.Fprintf(pm.logWriter, "✓ %s stopped gracefully\n", name)
	} else {
		fmt.Fprintf(pm.logWriter, "✓ %s stopped (forced)\n", name)
	}
	return nil
}

func checkProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(os.Signal(nil))
	return err == nil
}

func (pm *ProcessManager) startNode(name, configFile, logPath string) error {
	pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("%s.pid", name))
	cmd := exec.Command("./bin/orama-node", "--config", configFile)
	logFile, _ := os.Create(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", name, err)
	}

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	fmt.Fprintf(pm.logWriter, "✓ %s started (PID: %d)\n", strings.Title(name), cmd.Process.Pid)

	time.Sleep(1 * time.Second)
	return nil
}

func (pm *ProcessManager) startGateway(ctx context.Context) error {
	pidPath := filepath.Join(pm.pidsDir, "gateway.pid")
	logPath := filepath.Join(pm.oramaDir, "logs", "gateway.log")

	cmd := exec.Command("./bin/gateway", "--config", "gateway.yaml")
	logFile, _ := os.Create(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start gateway: %w", err)
	}

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	fmt.Fprintf(pm.logWriter, "✓ Gateway started (PID: %d, listen: 6001)\n", cmd.Process.Pid)

	return nil
}

func (pm *ProcessManager) startOlric(ctx context.Context) error {
	pidPath := filepath.Join(pm.pidsDir, "olric.pid")
	logPath := filepath.Join(pm.oramaDir, "logs", "olric.log")
	configPath := filepath.Join(pm.oramaDir, "olric-config.yaml")

	cmd := exec.CommandContext(ctx, "olric-server")
	cmd.Env = append(os.Environ(), fmt.Sprintf("OLRIC_SERVER_CONFIG=%s", configPath))
	logFile, _ := os.Create(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start olric: %w", err)
	}

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	fmt.Fprintf(pm.logWriter, "✓ Olric started (PID: %d)\n", cmd.Process.Pid)

	time.Sleep(1 * time.Second)
	return nil
}

func (pm *ProcessManager) startAnon(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return nil
	}

	pidPath := filepath.Join(pm.pidsDir, "anon.pid")
	logPath := filepath.Join(pm.oramaDir, "logs", "anon.log")

	cmd := exec.CommandContext(ctx, "npx", "anyone-client")
	logFile, _ := os.Create(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(pm.logWriter, "  ⚠️  Failed to start Anon: %v\n", err)
		return nil
	}

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	fmt.Fprintf(pm.logWriter, "✓ Anon proxy started (PID: %d, SOCKS: 9050)\n", cmd.Process.Pid)

	return nil
}

func (pm *ProcessManager) startMCP(ctx context.Context) error {
	topology := DefaultTopology()
	pidPath := filepath.Join(pm.pidsDir, "rqlite-mcp.pid")
	logPath := filepath.Join(pm.oramaDir, "logs", "rqlite-mcp.log")

	cmd := exec.CommandContext(ctx, "./bin/rqlite-mcp")
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("MCP_PORT=%d", topology.MCPPort),
		fmt.Sprintf("RQLITE_URL=http://localhost:%d", topology.Nodes[0].RQLiteHTTPPort),
	)
	logFile, _ := os.Create(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(pm.logWriter, "  ⚠️  Failed to start Rqlite MCP: %v\n", err)
		return nil
	}

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	fmt.Fprintf(pm.logWriter, "✓ Rqlite MCP started (PID: %d, port: %d)\n", cmd.Process.Pid, topology.MCPPort)

	return nil
}

func (pm *ProcessManager) startNodes(ctx context.Context) error {
	topology := DefaultTopology()
	for _, nodeSpec := range topology.Nodes {
		logPath := filepath.Join(pm.oramaDir, "logs", fmt.Sprintf("%s.log", nodeSpec.Name))
		if err := pm.startNode(nodeSpec.Name, nodeSpec.ConfigFilename, logPath); err != nil {
			return fmt.Errorf("failed to start %s: %w", nodeSpec.Name, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}
