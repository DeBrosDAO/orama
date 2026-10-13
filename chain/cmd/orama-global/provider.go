package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/provider"
)

// Provider defaults. The files live in the unit's WorkingDirectory, which is
// the provider's StateDirectory.
const (
	providerListen      = "0.0.0.0:31013"
	defaultIPFSAPI      = "http://127.0.0.1:31011"
	providerInterval    = 6 * time.Second
	providerMaxPiece    = 256 << 20
	providerRatePerSec  = 20
	providerBurst       = 40
	providerMaxIPs      = 4096
	providerReadTimeout = 2 * time.Minute
	shutdownTimeout     = 10 * time.Second
)

type providerFlags struct {
	listen, rpc, home  string
	ipfsAPI, ipfsToken string
	startHeight        int64
	interval           time.Duration
	maxPiece           int64
}

func providerCmd() *cobra.Command {
	var fl providerFlags
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Store assigned pieces, accept or decline them, and answer challenges",
		Long: `provider watches the chain for slots assigned to this node, accepts a slot once
its piece has been uploaded to POST /pieces/<hex piece root>, declines it before
the accept window closes when nothing arrived, proves every challenge each epoch,
and releases a slot one epoch after the chain stops naming this node for it.

PUBLIC_PIN and ARCHIVE slots are pinned in this host's public Kubo (--ipfs-api,
bearer token in --ipfs-token-file) before they are accepted, so anyone can fetch
the bytes by CID; a piece is also fetched by CID into the store through
POST /pins/<hex piece root> with X-Piece-CID, checked against the root. PRIVATE
slots never reach Kubo. Without --ipfs-api the provider does not pin.

Files in --home: hot-key (created on first start, mode 0600), node-id (the x/nodes
id, written after registration), denylist (optional, one CID per line), store/,
state.json and monitor.json.`,
		RunE: func(cmd *cobra.Command, _ []string) error { return runProvider(cmd.Context(), fl) },
	}
	f := cmd.Flags()
	f.StringVar(&fl.listen, "listen", providerListen, "Upload and retrieval HTTP address")
	f.StringVar(&fl.rpc, "rpc", defaultRPC, "oramad CometBFT RPC")
	f.StringVar(&fl.home, "home", ".", "Provider state directory")
	f.StringVar(&fl.ipfsAPI, "ipfs-api", "", "Public Kubo RPC, for example "+defaultIPFSAPI+" (empty: no public pinning)")
	f.StringVar(&fl.ipfsToken, "ipfs-token-file", "", "File holding the public Kubo RPC bearer token, with --ipfs-api")
	f.Int64Var(&fl.startHeight, "start-height", 0, "First block to read when state.json does not exist (default: the node's x/nodes registration height)")
	f.DurationVar(&fl.interval, "interval", providerInterval, "Time between chain steps")
	f.Int64Var(&fl.maxPiece, "max-piece-bytes", providerMaxPiece, "Largest upload accepted")
	return cmd
}

func runProvider(ctx context.Context, fl providerFlags) error {
	if fl.interval <= 0 || fl.maxPiece < 1 || fl.startHeight < 0 {
		return errors.New("--interval and --max-piece-bytes must be positive and --start-height not negative")
	}
	hot, created, err := loadOrCreateHotKey(filepath.Join(fl.home, "hot-key"))
	if err != nil {
		return err
	}
	if created {
		slog.Info("created the provider hot key; fund it and name it in x/nodes", "address", hot.Address)
	}
	nodeID, err := readNodeID(filepath.Join(fl.home, "node-id"))
	if err != nil {
		return err
	}
	deny, err := readDenylist(filepath.Join(fl.home, "denylist"))
	if err != nil {
		return err
	}
	storeDir := filepath.Join(fl.home, "store")
	store, err := provider.Open(storeDir, deny, func() (uint64, error) { return freeBytes(storeDir) })
	if err != nil {
		return fmt.Errorf("open the piece store %s: %w", storeDir, err)
	}
	kubo, err := openPublicKubo(fl)
	if err != nil {
		return err
	}
	var pins provider.Pinner
	if kubo != nil {
		pins = kubo
		store.SetUnpinner(kubo)
	}
	client, err := node.Dial(fl.rpc)
	if err != nil {
		return err
	}
	chain, err := provider.NewNodeChain(client, hot)
	if err != nil {
		return err
	}
	statePath := filepath.Join(fl.home, "state.json")
	start, err := startHeight(ctx, statePath, fl.startHeight, func(ctx context.Context) (int64, error) {
		return chain.RegisteredHeight(ctx, nodeID)
	})
	if err != nil {
		return err
	}
	runner, err := provider.NewRunner(store, chain, provider.Config{
		NodeID: nodeID, Signer: hot.Address, StartHeight: start, Pins: pins,
		StatePath: statePath, MonitorPath: filepath.Join(fl.home, "monitor.json"),
	})
	if err != nil {
		return err
	}
	handler, err := provider.NewRetrieval(store, providerRatePerSec, providerBurst, providerMaxIPs)
	if err != nil {
		return err
	}
	if err := handler.AcceptUploads(fl.maxPiece, runner.Assigned); err != nil {
		return err
	}
	if kubo != nil {
		if err := handler.AcceptPins(kubo, runner.AssignedPublic); err != nil {
			return err
		}
	}
	return serveAndStep(ctx, fl, handler, runner)
}

// openPublicKubo is the client for this host's public Kubo, or nil when
// --ipfs-api is empty. The token file is required with the API and is read
// once: it is the file the installer wrote in the Kubo home for the provider's group.
func openPublicKubo(fl providerFlags) (*provider.Kubo, error) {
	if fl.ipfsAPI == "" {
		if fl.ipfsToken != "" {
			return nil, errors.New("--ipfs-token-file needs --ipfs-api")
		}
		return nil, nil
	}
	if fl.ipfsToken == "" {
		return nil, errors.New("--ipfs-api needs --ipfs-token-file")
	}
	token, err := os.ReadFile(fl.ipfsToken)
	if err != nil {
		return nil, fmt.Errorf("read the public Kubo token %s (the provider must be in the orama-ipfs-pub-rpc group): %w", fl.ipfsToken, err)
	}
	return provider.NewKubo(fl.ipfsAPI, string(token))
}

func serveAndStep(ctx context.Context, fl providerFlags, handler http.Handler, runner *provider.Runner) error {
	srv := &http.Server{Addr: fl.listen, Handler: handler, ReadHeaderTimeout: providerReadTimeout / 4, ReadTimeout: providerReadTimeout}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("provider listening", "addr", fl.listen, "rpc", fl.rpc)
	tick := time.NewTicker(fl.interval)
	defer tick.Stop()
	for {
		if err := runner.Step(ctx); err != nil && ctx.Err() == nil {
			// The next step reads the chain again; a missed accept or proof is rebuilt then.
			slog.Error("provider step failed", "err", err)
		}
		select {
		case <-ctx.Done():
			shut, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			return srv.Shutdown(shut)
		case err := <-errc:
			return fmt.Errorf("provider HTTP on %s stopped: %w", fl.listen, err)
		case <-tick.C:
		}
	}
}

func readDenylist(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read denylist %s: %w", path, err)
	}
	return strings.Split(string(body), "\n"), nil
}

func freeBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", dir, err)
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// startHeight is the flag when set. Otherwise it is the node's registration
// height, read only when there is no state yet: a restart resumes from its
// cursor and does not need x/nodes to answer.
func startHeight(ctx context.Context, statePath string, flag int64, registered func(context.Context) (int64, error)) (int64, error) {
	if flag != 0 {
		return flag, nil
	}
	if _, err := os.Stat(statePath); err == nil {
		return 0, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("stat provider state %s: %w", statePath, err)
	}
	return registered(ctx)
}
