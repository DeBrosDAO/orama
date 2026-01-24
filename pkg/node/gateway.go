package node

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// startHTTPGateway initializes and starts the full API gateway
func (n *Node) startHTTPGateway(ctx context.Context) error {
	if !n.config.HTTPGateway.Enabled {
		n.logger.ComponentInfo(logging.ComponentNode, "HTTP Gateway disabled in config")
		return nil
	}

	logFile := filepath.Join(os.ExpandEnv(n.config.Node.DataDir), "..", "logs", "gateway.log")
	logsDir := filepath.Dir(logFile)
	_ = os.MkdirAll(logsDir, 0755)

	gatewayLogger, err := logging.NewFileLogger(logging.ComponentGeneral, logFile, false)
	if err != nil {
		return err
	}

	gwCfg := &gateway.Config{
		ListenAddr:        n.config.HTTPGateway.ListenAddr,
		ClientNamespace:   n.config.HTTPGateway.ClientNamespace,
		BootstrapPeers:    n.config.Discovery.BootstrapPeers,
		NodePeerID:        loadNodePeerIDFromIdentity(n.config.Node.DataDir),
		RQLiteDSN:         n.config.HTTPGateway.RQLiteDSN,
		OlricServers:      n.config.HTTPGateway.OlricServers,
		OlricTimeout:      n.config.HTTPGateway.OlricTimeout,
		IPFSClusterAPIURL: n.config.HTTPGateway.IPFSClusterAPIURL,
		IPFSAPIURL:        n.config.HTTPGateway.IPFSAPIURL,
		IPFSTimeout:       n.config.HTTPGateway.IPFSTimeout,
		EnableHTTPS:       n.config.HTTPGateway.HTTPS.Enabled,
		DomainName:        n.config.HTTPGateway.HTTPS.Domain,
		TLSCacheDir:       n.config.HTTPGateway.HTTPS.CacheDir,
		BaseDomain:        n.config.HTTPGateway.BaseDomain,
	}

	apiGateway, err := gateway.New(gatewayLogger, gwCfg)
	if err != nil {
		return err
	}
	n.apiGateway = apiGateway

	var certManager *autocert.Manager
	if gwCfg.EnableHTTPS && gwCfg.DomainName != "" {
		tlsCacheDir := gwCfg.TLSCacheDir
		if tlsCacheDir == "" {
			tlsCacheDir = "/home/debros/.orama/tls-cache"
		}
		_ = os.MkdirAll(tlsCacheDir, 0700)

		certManager = &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(gwCfg.DomainName),
			Cache:      autocert.DirCache(tlsCacheDir),
			Email:      fmt.Sprintf("admin@%s", gwCfg.DomainName),
			Client: &acme.Client{
				DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
			},
		}
		n.certManager = certManager
		n.certReady = make(chan struct{})
	}

	httpReady := make(chan struct{})

	go func() {
		if gwCfg.EnableHTTPS && gwCfg.DomainName != "" && certManager != nil {
			httpsPort := 443
			httpPort := 80

			httpServer := &http.Server{
				Addr: fmt.Sprintf(":%d", httpPort),
				Handler: certManager.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					target := fmt.Sprintf("https://%s%s", r.Host, r.URL.RequestURI())
					http.Redirect(w, r, target, http.StatusMovedPermanently)
				})),
			}

			httpListener, err := net.Listen("tcp", fmt.Sprintf(":%d", httpPort))
			if err != nil {
				close(httpReady)
				return
			}

			go httpServer.Serve(httpListener)

			// Pre-provision cert
			certReq := &tls.ClientHelloInfo{ServerName: gwCfg.DomainName}
			_, certErr := certManager.GetCertificate(certReq)
			
			if certErr != nil {
				close(httpReady)
				httpServer.Handler = apiGateway.Routes()
				return
			}

			close(httpReady)

			tlsConfig := &tls.Config{
				MinVersion:     tls.VersionTLS12,
				GetCertificate: certManager.GetCertificate,
			}

			httpsServer := &http.Server{
				Addr:      fmt.Sprintf(":%d", httpsPort),
				TLSConfig: tlsConfig,
				Handler:   apiGateway.Routes(),
			}
			n.apiGatewayServer = httpsServer

			ln, err := tls.Listen("tcp", fmt.Sprintf(":%d", httpsPort), tlsConfig)
			if err == nil {
				httpsServer.Serve(ln)
			}
		} else {
			close(httpReady)
			server := &http.Server{
				Addr:    gwCfg.ListenAddr,
				Handler: apiGateway.Routes(),
			}
			n.apiGatewayServer = server
			ln, err := net.Listen("tcp", gwCfg.ListenAddr)
			if err == nil {
				server.Serve(ln)
			}
		}
	}()

	// SNI Gateway
	if n.config.HTTPGateway.SNI.Enabled && n.certManager != nil {
		go n.startSNIGateway(ctx, httpReady)
	}

	return nil
}

func (n *Node) startSNIGateway(ctx context.Context, httpReady <-chan struct{}) {
	<-httpReady
	domain := n.config.HTTPGateway.HTTPS.Domain
	if domain == "" {
		return
	}

	certReq := &tls.ClientHelloInfo{ServerName: domain}
	tlsCert, err := n.certManager.GetCertificate(certReq)
	if err != nil {
		return
	}

	tlsCacheDir := n.config.HTTPGateway.HTTPS.CacheDir
	if tlsCacheDir == "" {
		tlsCacheDir = "/home/debros/.orama/tls-cache"
	}

	certPath := filepath.Join(tlsCacheDir, domain+".crt")
	keyPath := filepath.Join(tlsCacheDir, domain+".key")

	if err := extractPEMFromTLSCert(tlsCert, certPath, keyPath); err == nil {
		if n.certReady != nil {
			close(n.certReady)
		}
	}

	sniCfg := n.config.HTTPGateway.SNI
	sniGateway, err := gateway.NewTCPSNIGateway(n.logger, &sniCfg)
	if err == nil {
		n.sniGateway = sniGateway
		sniGateway.Start(ctx)
	}
}

// startIPFSClusterConfig initializes and ensures IPFS Cluster configuration
func (n *Node) startIPFSClusterConfig() error {
	n.logger.ComponentInfo(logging.ComponentNode, "Initializing IPFS Cluster configuration")

	cm, err := ipfs.NewClusterConfigManager(n.config, n.logger.Logger)
	if err != nil {
		return err
	}
	n.clusterConfigManager = cm

	_ = cm.FixIPFSConfigAddresses()
	if err := cm.EnsureConfig(); err != nil {
		return err
	}

	_ = cm.RepairPeerConfiguration()
	return nil
}

