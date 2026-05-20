// Package mobile is the gomobile bind target for Android.
package mobile

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"xproxy/internal/config"
	"xproxy/internal/device"
)

type HostResolver interface {
	LookupHost(hostname string) (string, error)
}

type ClientConfig struct {
	DeviceID            string
	ServerAddr          string
	HeartbeatIntervalNs int64
	ReconnectMinNs      int64
	ReconnectMaxNs      int64
	ProxyIdleTimeout    int64
	MaxConcurrent       int
	DNSCacheTTL         int64
}

var (
	stderrLog = log.New(os.Stderr, "xproxy: ", log.LstdFlags)
	runMu     sync.Mutex
	runCancel context.CancelFunc
	netType   atomic.Value

	netChangeMu sync.Mutex
	netChangeCh = make(chan struct{})
)

func init() {
	netType.Store("")
}

// SetNetworkType records the current network type. If the type actually
// changes, the active xproxy session is asked to reconnect immediately so the
// new uplink is used instead of waiting for keepalive to notice the old one.
func SetNetworkType(t string) {
	prev, _ := netType.Load().(string)
	netType.Store(t)
	if prev == "" || prev == t {
		return
	}
	netChangeMu.Lock()
	old := netChangeCh
	netChangeCh = make(chan struct{})
	netChangeMu.Unlock()
	close(old)
}

func currentNetChangeCh() <-chan struct{} {
	netChangeMu.Lock()
	defer netChangeMu.Unlock()
	return netChangeCh
}

// resolverLookupTimeout caps each Android-side DNS lookup. Without this the
// Java resolver can block 10–20s on a stalled uplink and stall every dial.
const resolverLookupTimeout = 5 * time.Second

// lookupHostWithTimeout runs the synchronous resolver in a goroutine and
// abandons it on ctx cancel / per-lookup timeout. The orphaned goroutine
// returns once the Android resolver finally errors out (channel is buffered).
func lookupHostWithTimeout(ctx context.Context, r HostResolver, host string, timeout time.Duration) ([]string, error) {
	type result struct {
		ips []string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := r.LookupHost(host)
		if err != nil {
			ch <- result{nil, err}
			return
		}
		ch <- result{device.ParseLookupLines(s), nil}
	}()

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-cctx.Done():
		return nil, fmt.Errorf("resolve %s: %w", host, cctx.Err())
	case r := <-ch:
		return r.ips, r.err
	}
}

func Run(cfg *ClientConfig, resolver HostResolver) error {
	c, err := toDeviceConfig(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	runMu.Lock()
	if runCancel != nil {
		runMu.Unlock()
		cancel()
		return fmt.Errorf("already running")
	}
	runCancel = cancel
	runMu.Unlock()

	defer func() {
		runMu.Lock()
		runCancel = nil
		runMu.Unlock()
		cancel()
	}()

	opts := &device.Options{
		NetType: func() string {
			v, _ := netType.Load().(string)
			return v
		},
		OnNetworkChange: currentNetChangeCh,
	}
	if resolver != nil {
		opts.Lookup = func(ctx context.Context, host string) ([]string, error) {
			return lookupHostWithTimeout(ctx, resolver, host, resolverLookupTimeout)
		}
	}

	return device.RunWith(ctx, c, stderrLog, opts)
}

func Stop() {
	runMu.Lock()
	c := runCancel
	runMu.Unlock()
	if c != nil {
		c()
	}
}

func toDeviceConfig(cfg *ClientConfig) (*config.Device, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cfg is nil")
	}
	if cfg.DeviceID == "" {
		return nil, fmt.Errorf("device_id required")
	}
	if cfg.ServerAddr == "" {
		return nil, fmt.Errorf("server_addr required")
	}
	c := &config.Device{
		DeviceID:          cfg.DeviceID,
		ServerAddr:        cfg.ServerAddr,
		HeartbeatInterval: time.Duration(cfg.HeartbeatIntervalNs),
		ReconnectMin:      time.Duration(cfg.ReconnectMinNs),
		ReconnectMax:      time.Duration(cfg.ReconnectMaxNs),
		ProxyIdleTimeout:  time.Duration(cfg.ProxyIdleTimeout),
		MaxConcurrent:     cfg.MaxConcurrent,
		DNSCacheTTL:       time.Duration(cfg.DNSCacheTTL),
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 10 * time.Second
	}
	if c.ReconnectMin == 0 {
		c.ReconnectMin = time.Second
	}
	if c.ReconnectMax == 0 {
		c.ReconnectMax = 60 * time.Second
	}
	if c.ReconnectMax < c.ReconnectMin {
		c.ReconnectMax = c.ReconnectMin
	}
	if c.ProxyIdleTimeout == 0 {
		c.ProxyIdleTimeout = 30 * time.Second
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 128
	}
	if c.DNSCacheTTL == 0 {
		c.DNSCacheTTL = 30 * time.Second
	}
	return c, nil
}
