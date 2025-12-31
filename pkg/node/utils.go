package node

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	mathrand "math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/multiformats/go-multiaddr"
)

func extractIPFromMultiaddr(multiaddrStr string) string {
	ma, err := multiaddr.NewMultiaddr(multiaddrStr)
	if err != nil {
		return ""
	}

	var ip string
	var dnsName string
	multiaddr.ForEach(ma, func(c multiaddr.Component) bool {
		switch c.Protocol().Code {
		case multiaddr.P_IP4, multiaddr.P_IP6:
			ip = c.Value()
			return false
		case multiaddr.P_DNS4, multiaddr.P_DNS6, multiaddr.P_DNSADDR:
			dnsName = c.Value()
		}
		return true
	})

	if ip != "" {
		return ip
	}

	if dnsName != "" {
		if resolvedIPs, err := net.LookupIP(dnsName); err == nil && len(resolvedIPs) > 0 {
			for _, resolvedIP := range resolvedIPs {
				if resolvedIP.To4() != nil {
					return resolvedIP.String()
				}
			}
			return resolvedIPs[0].String()
		}
	}

	return ""
}

func calculateNextBackoff(current time.Duration) time.Duration {
	next := time.Duration(float64(current) * 1.5)
	maxInterval := 10 * time.Minute
	if next > maxInterval {
		next = maxInterval
	}
	return next
}

func addJitter(interval time.Duration) time.Duration {
	jitterPercent := 0.2
	jitterRange := float64(interval) * jitterPercent
	jitter := (mathrand.Float64() - 0.5) * 2 * jitterRange
	result := time.Duration(float64(interval) + jitter)
	if result < time.Second {
		result = time.Second
	}
	return result
}

func loadNodePeerIDFromIdentity(dataDir string) string {
	identityFile := filepath.Join(os.ExpandEnv(dataDir), "identity.key")
	if strings.HasPrefix(identityFile, "~") {
		home, _ := os.UserHomeDir()
		identityFile = filepath.Join(home, identityFile[1:])
	}

	if info, err := encryption.LoadIdentity(identityFile); err == nil {
		return info.PeerID.String()
	}
	return ""
}

func extractPEMFromTLSCert(tlsCert *tls.Certificate, certPath, keyPath string) error {
	if tlsCert == nil || len(tlsCert.Certificate) == 0 {
		return fmt.Errorf("invalid tls certificate")
	}

	certFile, err := os.Create(certPath)
	if err != nil {
		return err
	}
	defer certFile.Close()

	for _, certBytes := range tlsCert.Certificate {
		pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	}

	if tlsCert.PrivateKey == nil {
		return fmt.Errorf("private key is nil")
	}

	keyFile, err := os.Create(keyPath)
	if err != nil {
		return err
	}
	defer keyFile.Close()

	var keyBytes []byte
	switch key := tlsCert.PrivateKey.(type) {
	case *x509.Certificate:
		keyBytes, _ = x509.MarshalPKCS8PrivateKey(key)
	default:
		keyBytes, _ = x509.MarshalPKCS8PrivateKey(tlsCert.PrivateKey)
	}

	pem.Encode(keyFile, &pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	os.Chmod(certPath, 0644)
	os.Chmod(keyPath, 0600)
	return nil
}

