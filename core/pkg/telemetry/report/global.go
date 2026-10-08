package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

const globalMonitorLimit = 4096

// Paths and the Kubo call are variables so tests can stand in for the node.
var (
	publicKuboTokenPath = filepath.Join(constants.GlobalIPFSHome, constants.GlobalIPFSAPITokenFile)
	providerMonitorPath = filepath.Join(constants.GlobalProviderHome, constants.GlobalMonitorFile)
	relayMonitorPath    = filepath.Join(constants.GlobalRelayHome, constants.GlobalMonitorFile)
	globalSystemctl     = chainSystemctl
	globalIPFSPost      = postBearer
)

// globalIPFSAPI is the public Kubo RPC as this machine's host reaches it: loopback, or on a
// co-located machine the namespace address. Like chainEndpoints it is read for each collection.
// It is a variable so tests can stand in for the node.
var globalIPFSAPI = func() string {
	if colocatedGlobal() {
		return constants.ColocatedGlobalIPFSAPIURL()
	}
	return constants.LocalGlobalIPFSAPIURL()
}

// collectGlobal reports public Kubo, the provider, and the relay when those
// units are installed. A cluster node has none of them, and the section is nil.
func collectGlobal() *GlobalReport {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	units := []string{constants.GlobalIPFSUnit, constants.GlobalProviderUnit, constants.GlobalRelayUnit}
	var g GlobalReport
	installed := 0
	for _, name := range units {
		ok, state, err := globalUnit(ctx, name)
		if err != nil {
			g.Error = shortChainError(fmt.Errorf("read %s: %w", name, err))
			return &g
		}
		if !ok {
			continue
		}
		installed++
		g.Units = append(g.Units, GlobalUnit{Name: name, State: state})
	}
	if installed == 0 && g.Error == "" {
		return nil
	}
	if state, ok := globalUnitState(&g, constants.GlobalIPFSUnit); ok && state == "active" {
		g.PublicIPFS = readPublicIPFS(ctx)
	}
	if _, ok := globalUnitState(&g, constants.GlobalProviderUnit); ok {
		g.Provider = readProviderMonitor()
	}
	if _, ok := globalUnitState(&g, constants.GlobalRelayUnit); ok {
		g.Relay = readRelayMonitor()
	}
	return &g
}

func globalUnit(ctx context.Context, name string) (bool, string, error) {
	load, err := globalSystemctl(ctx, "show", "-p", "LoadState", "--value", name)
	if err != nil {
		return false, "", err
	}
	if load == "" || load == chainUnitNotFound {
		return false, "", nil
	}
	state, err := globalSystemctl(ctx, "is-active", name)
	if err != nil || state == "" {
		state = "inactive"
	}
	return true, state, nil
}

func globalUnitState(g *GlobalReport, name string) (string, bool) {
	if g == nil {
		return "", false
	}
	for _, u := range g.Units {
		if u.Name == name {
			return u.State, true
		}
	}
	return "", false
}

func readPublicIPFS(ctx context.Context) *PublicIPFSReport {
	r := &PublicIPFSReport{}
	token, err := readToken(publicKuboTokenPath)
	if err != nil {
		r.Error = shortChainError(err)
		return r
	}
	body, err := globalIPFSPost(ctx, globalIPFSAPI()+"/api/v0/repo/stat", token)
	if err != nil {
		r.Error = shortChainError(err)
		return r
	}
	repo, max, err := parseRepoStat(body)
	if err != nil {
		r.Error = shortChainError(err)
		return r
	}
	r.RepoBytes = repo
	r.StorageMaxBytes = max
	return r
}

func readToken(path string) (string, error) {
	data, err := readCapped(path, 256)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("public Kubo API token is missing")
		}
		return "", fmt.Errorf("public Kubo API token is not readable")
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", fmt.Errorf("public Kubo API token is empty")
	}
	return token, nil
}

func readProviderMonitor() *ProviderReport {
	r := &ProviderReport{}
	mon, present, err := readMonitor(providerMonitorPath)
	if err != nil {
		r.Error = shortChainError(err)
		return r
	}
	if !present {
		return r
	}
	r.HotKeyBalanceNorama = mon.HotKeyBalanceNorama
	r.ProofMisses = mon.ProofMisses
	r.DiskBytes = mon.DiskBytes
	r.StorageMaxBytes = mon.StorageMaxBytes
	r.HeldSlots = mon.HeldSlots
	r.PendingSlots = mon.PendingSlots
	return r
}

func readRelayMonitor() *RelayReport {
	r := &RelayReport{}
	mon, present, err := readMonitor(relayMonitorPath)
	if err != nil {
		r.Error = shortChainError(err)
		return r
	}
	if !present {
		return r
	}
	r.InConsensus = mon.InConsensus
	return r
}

// MonitorFile is /var/lib/orama-global/{provider,relay}/monitor.json.
// Absent fields stay nil. The storage provider writes its file; no process
// writes the relay's yet, so a relay's fields stay absent.
type MonitorFile struct {
	HotKeyBalanceNorama *int64 `json:"hot_key_balance_norama"`
	ProofMisses         *int   `json:"proof_misses"`
	DiskBytes           *int64 `json:"disk_bytes"`
	StorageMaxBytes     *int64 `json:"storage_max_bytes"`
	HeldSlots           *int   `json:"held_slots"`
	PendingSlots        *int   `json:"pending_slots"`
	InConsensus         *bool  `json:"in_consensus"`
}

// ParseMonitor reads a monitor file body. ParseRepoStat reads a Kubo repo/stat body.
func ParseMonitor(data []byte) (MonitorFile, error) { return parseMonitor(data) }

// ParseRepoStat reads a Kubo /api/v0/repo/stat body.
func ParseRepoStat(body []byte) (int64, int64, error) { return parseRepoStat(body) }

func readMonitor(path string) (MonitorFile, bool, error) {
	var mon MonitorFile
	data, err := readCapped(path, globalMonitorLimit)
	if err != nil {
		if os.IsNotExist(err) {
			return mon, false, nil
		}
		return mon, false, fmt.Errorf("read the monitor file")
	}
	mon, err = parseMonitor(data)
	if err != nil {
		return MonitorFile{}, true, err
	}
	return mon, true, nil
}

func parseMonitor(data []byte) (MonitorFile, error) {
	var mon MonitorFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&mon); err != nil {
		return MonitorFile{}, fmt.Errorf("monitor file is not valid JSON")
	}
	if mon.HotKeyBalanceNorama != nil && *mon.HotKeyBalanceNorama < 0 {
		return mon, fmt.Errorf("monitor file has a negative hot-key balance")
	}
	if mon.ProofMisses != nil && *mon.ProofMisses < 0 {
		return mon, fmt.Errorf("monitor file has a negative proof-miss count")
	}
	if mon.DiskBytes != nil && *mon.DiskBytes < 0 {
		return mon, fmt.Errorf("monitor file has a negative disk size")
	}
	if mon.StorageMaxBytes != nil && *mon.StorageMaxBytes < 0 {
		return mon, fmt.Errorf("monitor file has a negative storage maximum")
	}
	if mon.HeldSlots != nil && *mon.HeldSlots < 0 {
		return mon, fmt.Errorf("monitor file has a negative held-slot count")
	}
	if mon.PendingSlots != nil && *mon.PendingSlots < 0 {
		return mon, fmt.Errorf("monitor file has a negative pending-slot count")
	}
	return mon, nil
}

func parseRepoStat(body []byte) (int64, int64, error) {
	var resp struct {
		RepoSize   int64 `json:"RepoSize"`
		StorageMax int64 `json:"StorageMax"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, 0, fmt.Errorf("public Kubo repo stat is not JSON")
	}
	if resp.RepoSize < 0 || resp.StorageMax < 0 {
		return 0, 0, fmt.Errorf("public Kubo repo stat has a negative size")
	}
	return resp.RepoSize, resp.StorageMax, nil
}

func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", filepath.Base(path), limit)
	}
	return data, nil
}

// postBearer POSTs an empty body. The token is a header and is never put in an error.
func postBearer(ctx context.Context, url, token string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, localHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readLocalBody(resp.Body, url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d from the public Kubo RPC", resp.StatusCode)
	}
	return body, nil
}
