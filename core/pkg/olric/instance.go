package olric

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// InstanceNodeStatus represents the status of an instance (local type to avoid import cycle)
type InstanceNodeStatus string

const (
	InstanceStatusPending  InstanceNodeStatus = "pending"
	InstanceStatusStarting InstanceNodeStatus = "starting"
	InstanceStatusRunning  InstanceNodeStatus = "running"
	InstanceStatusStopped  InstanceNodeStatus = "stopped"
	InstanceStatusFailed   InstanceNodeStatus = "failed"
)

// OlricInstance is a namespace's Olric on one node, as the cluster manager
// records it. The units are started by systemd (pkg/namespace SpawnOlric),
// which also writes their config.
type OlricInstance struct {
	Namespace      string
	NodeID         string
	HTTPPort       int
	MemberlistPort int
	BindAddr       string
	AdvertiseAddr  string
	PeerAddresses  []string // Memberlist peer addresses for cluster discovery
	ConfigPath     string
	PID            int
	StartedAt      time.Time

	// mu protects mutable state (Status, LastHealthCheck) accessed concurrently.
	mu              sync.RWMutex
	Status          InstanceNodeStatus
	LastHealthCheck time.Time
}

// InstanceConfig holds configuration for spawning an Olric instance
type InstanceConfig struct {
	Namespace      string   // Namespace name (e.g., "alice")
	NodeID         string   // Physical node ID
	HTTPPort       int      // HTTP API port
	MemberlistPort int      // Memberlist gossip port
	BindAddr       string   // Address to bind (e.g., "0.0.0.0")
	AdvertiseAddr  string   // Address to advertise (e.g., "192.168.1.10")
	PeerAddresses  []string // Memberlist peer addresses for initial cluster join
}

// AdvertisedDSN returns the advertised connection address
func (oi *OlricInstance) AdvertisedDSN() string {
	return fmt.Sprintf("%s:%d", oi.AdvertiseAddr, oi.HTTPPort)
}

// IsHealthy reports whether the instance's memberlist port accepts a
// connection, the sign Olric is up and can join its peers.
func (oi *OlricInstance) IsHealthy(ctx context.Context) (bool, error) {
	addr := net.JoinHostPort(oi.AdvertiseAddr, strconv.Itoa(oi.MemberlistPort))
	if oi.AdvertiseAddr == "" || oi.AdvertiseAddr == "0.0.0.0" {
		addr = net.JoinHostPort("localhost", strconv.Itoa(oi.MemberlistPort))
	}
	var d net.Dialer
	dialCtx, cancel := context.WithTimeout(ctx, memberlistDialTimeout)
	defer cancel()
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return false, fmt.Errorf("olric memberlist %s not accepting connections: %w", addr, err)
	}
	conn.Close()
	return true, nil
}

// memberlistDialTimeout bounds IsHealthy's connection attempt.
const memberlistDialTimeout = 2 * time.Second
