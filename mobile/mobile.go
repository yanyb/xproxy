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
}

var (
	stderrLog = log.New(os.Stderr, "xproxy: ", log.LstdFlags)
	runMu     sync.Mutex
	runCancel context.CancelFunc
	netType   atomic.Value
)

func init() {
	netType.Store("")
}

func SetNetworkType(t string) {
	netType.Store(t)
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

	var opts *device.Options
	if resolver != nil {
		opts = &device.Options{
			Lookup: func(ctx context.Context, host string) ([]string, error) {
				s, err := resolver.LookupHost(host)
				if err != nil {
					return nil, err
				}
				return device.ParseLookupLines(s), nil
			},
			NetType: func() string {
				v, _ := netType.Load().(string)
				return v
			},
		}
	} else {
		opts = &device.Options{
			NetType: func() string {
				v, _ := netType.Load().(string)
				return v
			},
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
	return c, nil
}
