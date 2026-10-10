package p2p

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("pkg", "p2p")

// PeerDiscovery resolves peers from DNS SRV, DNS A, and static list sources.
type PeerDiscovery struct {
	dnsSRV      string
	dnsA        string
	staticList  string
	defaultPort int

	mu    sync.RWMutex
	peers []string // cached last resolve
}

// NewPeerDiscovery creates a PeerDiscovery with the given sources and default port.
func NewPeerDiscovery(dnsSRV, dnsA, staticList string, defaultPort int) *PeerDiscovery {
	return &PeerDiscovery{
		dnsSRV:      dnsSRV,
		dnsA:        dnsA,
		staticList:  staticList,
		defaultPort: defaultPort,
	}
}

// Resolve queries all configured sources, deduplicates, caches and returns the result.
func (d *PeerDiscovery) Resolve(ctx context.Context) ([]string, error) {
	seen := make(map[string]struct{})
	var result []string

	add := func(addrs []string) {
		for _, a := range addrs {
			if _, ok := seen[a]; !ok {
				seen[a] = struct{}{}
				result = append(result, a)
			}
		}
	}

	// 1. Static peers
	add(parseStaticPeers(d.staticList))

	// 2. DNS A
	if d.dnsA != "" {
		addrs, err := resolveDNSA(ctx, d.dnsA, d.defaultPort)
		if err != nil {
			logger.WithError(err).Warnf("DNS A lookup failed for %s", d.dnsA)
		} else {
			add(addrs)
		}
	}

	// 3. DNS SRV
	if d.dnsSRV != "" {
		addrs, err := resolveDNSSRV(ctx, d.dnsSRV)
		if err != nil {
			logger.WithError(err).Warnf("DNS SRV lookup failed for %s", d.dnsSRV)
		} else {
			add(addrs)
		}
	}

	// Cache result
	d.mu.Lock()
	d.peers = result
	d.mu.Unlock()

	return result, nil
}

// Peers returns a copy of the last resolved peer list (thread-safe).
func (d *PeerDiscovery) Peers() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.peers) == 0 {
		return nil
	}
	cp := make([]string, len(d.peers))
	copy(cp, d.peers)
	return cp
}

// RunLoop periodically calls Resolve until ctx is cancelled.
func (d *PeerDiscovery) RunLoop(ctx context.Context, interval time.Duration) {
	// Resolve immediately on first call
	if _, err := d.Resolve(ctx); err != nil {
		logger.WithError(err).Warn("peer discovery resolve failed")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := d.Resolve(ctx); err != nil {
				logger.WithError(err).Warn("peer discovery resolve failed")
			}
		}
	}
}

// parseStaticPeers splits a comma-separated list, trims whitespace, and skips empty entries.
func parseStaticPeers(list string) []string {
	if list == "" {
		return nil
	}
	parts := strings.Split(list, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveDNSA resolves host via DNS A lookup and returns addresses formatted as "ip:port".
func resolveDNSA(ctx context.Context, host string, port int) ([]string, error) {
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(addrs))
	for _, a := range addrs {
		result = append(result, fmt.Sprintf("%s:%d", a, port))
	}
	return result, nil
}

// resolveDNSSRV resolves a DNS SRV record and returns addresses formatted as "target:port".
func resolveDNSSRV(ctx context.Context, name string) ([]string, error) {
	_, addrs, err := net.DefaultResolver.LookupSRV(ctx, "", "", name)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(addrs))
	for _, srv := range addrs {
		target := strings.TrimSuffix(srv.Target, ".")
		result = append(result, fmt.Sprintf("%s:%d", target, srv.Port))
	}
	return result, nil
}
