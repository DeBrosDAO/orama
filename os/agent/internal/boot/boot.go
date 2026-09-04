// Package boot orchestrates the OramaOS agent boot sequence.
//
// Two modes:
//   - Enrollment mode (first boot): HTTP server on :9999, WG setup, LUKS format, share distribution
//   - Standard boot (subsequent): WG up, LUKS unlock via Shamir shares, start services
package boot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/DeBrosOfficial/orama-os/agent/internal/command"
	"github.com/DeBrosOfficial/orama-os/agent/internal/enroll"
	"github.com/DeBrosOfficial/orama-os/agent/internal/health"
	"github.com/DeBrosOfficial/orama-os/agent/internal/sandbox"
	"github.com/DeBrosOfficial/orama-os/agent/internal/update"
	"github.com/DeBrosOfficial/orama-os/agent/internal/wireguard"
)

const (
	// OramaDir is the base data directory, mounted from the LUKS-encrypted partition.
	OramaDir = "/opt/orama/.orama"

	// EnrolledFlag indicates that this node has completed enrollment.
	EnrolledFlag = "/opt/orama/.orama/enrolled"

	// DataDevice is the LUKS-encrypted data partition.
	DataDevice = "/dev/sda3"

	// DataMapperName is the device-mapper name for the unlocked LUKS partition.
	DataMapperName = "orama-data"

	// DataMountPoint is where the decrypted data partition is mounted.
	DataMountPoint = "/opt/orama/.orama"

	// WireGuardConfigPath is the path to the WireGuard configuration baked into rootfs
	// during enrollment, or written during first boot.
	WireGuardConfigPath = "/etc/wireguard/wg0.conf"

	// GatewayEndpoint is the default gateway URL for enrollment WebSocket.
	// Overridden by /etc/orama/gateway-url if present.
	GatewayEndpoint = "wss://gateway.orama.network/v1/agent/enroll"
)

// Agent is the main orchestrator for the OramaOS node.
type Agent struct {
	wg         *wireguard.Manager
	supervisor *sandbox.Supervisor
	updater    *update.Manager
	cmdRecv    *command.Receiver
	reporter   *health.Reporter

	// agentToken is this node's own credential, minted at enrollment and
	// required on every command the gateway sends. Empty on a node enrolled
	// before it existed, which means no command receiver.
	agentToken string

	mu       sync.Mutex
	shutdown bool
}

// NewAgent creates a new Agent instance.
func NewAgent() (*Agent, error) {
	return &Agent{
		wg: wireguard.NewManager(),
	}, nil
}

// Run executes the boot sequence. It detects whether this is a first boot
// (enrollment) or a standard boot, and acts accordingly.
func (a *Agent) Run() error {
	if isEnrolled() {
		return a.standardBoot()
	}
	return a.enrollmentBoot()
}

// isEnrolled checks if the node has completed enrollment.
func isEnrolled() bool {
	_, err := os.Stat(EnrolledFlag)
	return err == nil
}

// enrollmentBoot handles first-boot enrollment.
func (a *Agent) enrollmentBoot() error {
	log.Println("ENROLLMENT MODE: first boot detected")

	// 1. Start enrollment server on port 9999
	enrollServer := enroll.NewServer(resolveGatewayEndpoint())
	result, agentToken, err := enrollServer.Run()
	if err != nil {
		return fmt.Errorf("enrollment failed: %w", err)
	}
	if err := a.storeAgentToken(agentToken); err != nil {
		return fmt.Errorf("enrollment succeeded but this node's agent token could not be "+
			"stored, so it would not survive a reboot: %w", err)
	}

	log.Println("enrollment complete, configuring node")

	// 2. Configure WireGuard with received config
	if err := a.wg.Configure(result.WireGuardConfig); err != nil {
		return fmt.Errorf("failed to configure WireGuard: %w", err)
	}
	if err := a.wg.Up(); err != nil {
		return fmt.Errorf("failed to bring up WireGuard: %w", err)
	}

	// 3. Generate LUKS key, format, and encrypt data partition
	luksKey, err := GenerateLUKSKey()
	if err != nil {
		return fmt.Errorf("failed to generate LUKS key: %w", err)
	}

	if err := FormatAndEncrypt(DataDevice, luksKey); err != nil {
		ZeroBytes(luksKey)
		return fmt.Errorf("failed to format LUKS partition: %w", err)
	}

	// 4. Distribute LUKS key shares to peer vault-guardians
	if err := DistributeKeyShares(luksKey, result.Peers, result.NodeID); err != nil {
		ZeroBytes(luksKey)
		return fmt.Errorf("failed to distribute key shares: %w", err)
	}
	ZeroBytes(luksKey)

	// 5. FormatAndEncrypt already mounted the partition — no need to decrypt again.

	// 6. Write enrolled flag
	if err := os.MkdirAll(filepath.Dir(EnrolledFlag), 0755); err != nil {
		return fmt.Errorf("failed to create enrolled flag dir: %w", err)
	}
	if err := os.WriteFile(EnrolledFlag, []byte("1"), 0644); err != nil {
		return fmt.Errorf("failed to write enrolled flag: %w", err)
	}

	log.Println("enrollment complete, proceeding to standard boot")

	// 7. Start services
	return a.startServices()
}

// standardBoot handles normal reboot sequence.
func (a *Agent) standardBoot() error {
	log.Println("STANDARD BOOT: enrolled node")

	// The token was written at enrollment. A node that predates it keeps
	// serving; it just cannot be commanded until it is re-enrolled, which is
	// better than a receiver that listens on every interface with no
	// credential.
	if err := a.loadAgentToken(); err != nil {
		log.Printf("no agent token on this node, so no command receiver will start: %v", err)
	}

	// 1. Bring up WireGuard
	if err := a.wg.Up(); err != nil {
		return fmt.Errorf("failed to bring up WireGuard: %w", err)
	}

	// 2. Try Shamir-based LUKS key reconstruction
	luksKey, err := FetchAndReconstruct(a.wg)
	if err != nil {
		// Shamir failed — fall back to genesis unlock mode.
		// This happens when the genesis node reboots before enough peers
		// have joined for Shamir distribution, or when peers are offline.
		log.Printf("Shamir reconstruction failed: %v", err)
		log.Println("Entering genesis unlock mode — waiting for operator unlock via WireGuard")

		luksKey, err = a.waitForGenesisUnlock()
		if err != nil {
			return fmt.Errorf("genesis unlock failed: %w", err)
		}
	}

	// 3. Decrypt and mount data partition
	if err := DecryptAndMount(DataDevice, luksKey); err != nil {
		ZeroBytes(luksKey)
		return fmt.Errorf("failed to mount data partition: %w", err)
	}
	ZeroBytes(luksKey)

	// 4. Mark boot as successful (A/B boot counting)
	if err := update.MarkBootSuccessful(); err != nil {
		log.Printf("WARNING: failed to mark boot successful: %v", err)
	}

	// 5. Start services
	return a.startServices()
}

// waitForGenesisUnlock starts a temporary HTTP server on the WireGuard interface
// (port 9998) that accepts a LUKS key from the operator.
// The operator sends: POST /v1/agent/unlock with {"key":"<base64-luks-key>"}
func (a *Agent) waitForGenesisUnlock() ([]byte, error) {
	keyCh := make(chan []byte, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent/unlock", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Key string `json:"key"` // base64-encoded LUKS key
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		keyBytes, err := base64.StdEncoding.DecodeString(req.Key)
		if err != nil {
			http.Error(w, "invalid base64 key", http.StatusBadRequest)
			return
		}

		if len(keyBytes) != 32 {
			http.Error(w, "key must be 32 bytes", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "unlocking"})

		keyCh <- keyBytes
	})

	server := &http.Server{
		Addr:         ":9998",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("genesis unlock server error: %w", err)
		}
	}()

	log.Println("Genesis unlock server listening on :9998")
	log.Println("Run 'orama node unlock --genesis --node-ip <wg-ip>' to unlock this node")

	select {
	case key := <-keyCh:
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(ctx)
		return key, nil
	case err := <-errCh:
		return nil, err
	}
}

// startServices launches all node services in sandboxes and starts background tasks.
func (a *Agent) startServices() error {
	// Start service supervisor
	a.supervisor = sandbox.NewSupervisor()
	if err := a.supervisor.StartAll(); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	// Start command receiver, bound to this node's WireGuard address and
	// authenticated by its agent token. Both come from enrollment; a node
	// enrolled before either existed has neither, and the receiver refuses to
	// start rather than listening on everything with no credential.
	wgIP, err := a.wg.LocalIP()
	if err != nil {
		return fmt.Errorf("cannot start the command receiver: this node's WireGuard address "+
			"is unknown, so the receiver would have to bind every interface: %w", err)
	}
	a.cmdRecv = command.NewReceiver(a.supervisor, wgIP, a.agentToken)
	go a.cmdRecv.Listen()

	// Start update checker (periodic)
	a.updater = update.NewManager()
	go a.updater.RunLoop()

	// Start health reporter (periodic)
	a.reporter = health.NewReporter(a.supervisor)
	go a.reporter.RunLoop()

	return nil
}

// Shutdown gracefully stops all services.
func (a *Agent) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.shutdown {
		return
	}
	a.shutdown = true

	log.Println("shutting down agent")

	if a.cmdRecv != nil {
		a.cmdRecv.Stop()
	}
	if a.updater != nil {
		a.updater.Stop()
	}
	if a.reporter != nil {
		a.reporter.Stop()
	}
	if a.supervisor != nil {
		a.supervisor.StopAll()
	}
}

// resolveGatewayEndpoint reads the gateway URL from config or uses the default.
func resolveGatewayEndpoint() string {
	data, err := os.ReadFile("/etc/orama/gateway-url")
	if err == nil {
		return string(data)
	}
	return GatewayEndpoint
}
