package device

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// dnsCacheEntry holds resolved IPs and an expiry stamp.
type dnsCacheEntry struct {
	ips []string
	exp time.Time
}

// dnsCache is a tiny in-memory TTL cache for HostLookup results. Crawlers
// repeatedly hit the same hosts; a 30s cache typically eliminates >90% of DNS
// round-trips while still picking up changes within one TTL window.
type dnsCache struct {
	mu      sync.Mutex
	entries map[string]dnsCacheEntry
	ttl     time.Duration

	// inflight collapses concurrent lookups of the same host into one upstream
	// resolution; followers wait on the same channel.
	inflight map[string]*dnsInflight
}

type dnsInflight struct {
	done chan struct{}
	ips  []string
	err  error
}

func newDNSCache(ttl time.Duration) *dnsCache {
	return &dnsCache{
		entries:  make(map[string]dnsCacheEntry),
		ttl:      ttl,
		inflight: make(map[string]*dnsInflight),
	}
}

func (c *dnsCache) lookup(ctx context.Context, host string, upstream HostLookup) ([]string, error) {
	now := time.Now()

	c.mu.Lock()
	if e, ok := c.entries[host]; ok && now.Before(e.exp) {
		ips := shuffleCopy(e.ips)
		c.mu.Unlock()
		return ips, nil
	}
	if inf, ok := c.inflight[host]; ok {
		c.mu.Unlock()
		select {
		case <-inf.done:
			if inf.err != nil {
				return nil, inf.err
			}
			return shuffleCopy(inf.ips), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	inf := &dnsInflight{done: make(chan struct{})}
	c.inflight[host] = inf
	c.mu.Unlock()

	ips, err := upstream(ctx, host)

	c.mu.Lock()
	if err == nil && len(ips) > 0 {
		c.entries[host] = dnsCacheEntry{ips: append([]string(nil), ips...), exp: time.Now().Add(c.ttl)}
	}
	inf.ips = ips
	inf.err = err
	delete(c.inflight, host)
	c.mu.Unlock()
	close(inf.done)

	if err != nil {
		return nil, err
	}
	return shuffleCopy(ips), nil
}

// shuffleCopy returns a Fisher-Yates shuffled copy so cached load-balanced
// records don't pin a single IP across calls.
func shuffleCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := len(out) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// withDNSCache wraps a HostLookup with a TTL cache. ttl<=0 returns orig unchanged.
func withDNSCache(orig HostLookup, ttl time.Duration) HostLookup {
	if ttl <= 0 || orig == nil {
		return orig
	}
	c := newDNSCache(ttl)
	return func(ctx context.Context, host string) ([]string, error) {
		return c.lookup(ctx, host, orig)
	}
}
