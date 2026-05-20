package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Server struct {
	SocksListen         string        `yaml:"socks_listen"`
	DeviceListen        string        `yaml:"device_listen"`
	TLSCert             string        `yaml:"tls_cert"`
	TLSKey              string        `yaml:"tls_key"`
	SocksPassword       string        `yaml:"socks_password"`
	DeviceWait          time.Duration `yaml:"device_wait"`
	ConnectWait         time.Duration `yaml:"connect_wait"`
	HeartbeatTTL        time.Duration `yaml:"heartbeat_ttl"`
	ProxyIdleTimeout    time.Duration `yaml:"proxy_idle_timeout"`
	SocksLocalResolve   bool          `yaml:"socks_local_resolve"`
	MaxClients          int           `yaml:"max_clients"`            // global cap on concurrent SOCKS clients (0 = unlimited)
	MaxClientsPerDevice int           `yaml:"max_clients_per_device"` // cap per device (0 = unlimited)
}

type Device struct {
	DeviceID          string        `yaml:"device_id"`
	ServerAddr        string        `yaml:"server_addr"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	ReconnectMin      time.Duration `yaml:"reconnect_min"`
	ReconnectMax      time.Duration `yaml:"reconnect_max"`
	ProxyIdleTimeout  time.Duration `yaml:"proxy_idle_timeout"`
	MaxConcurrent     int           `yaml:"max_concurrent"`
	DNSCacheTTL       time.Duration `yaml:"dns_cache_ttl"` // 0 disables cache; crawler-friendly default 30s
}

func LoadServer(path string) (*Server, error) {
	var c Server
	if err := loadYAML(path, &c); err != nil {
		return nil, err
	}
	if c.DeviceWait == 0 {
		c.DeviceWait = 30 * time.Second
	}
	if c.ConnectWait == 0 {
		c.ConnectWait = 30 * time.Second
	}
	if c.ProxyIdleTimeout == 0 {
		// Crawler workloads see short-lived requests; 30s is enough to span typical
		// HTTP think-time and lets idle relays be reclaimed quickly.
		c.ProxyIdleTimeout = 30 * time.Second
	}
	if c.HeartbeatTTL == 0 {
		// Without this, ServeDevice skips watchHeartbeat and silent half-open
		// device connections accumulate in the Registry forever.
		c.HeartbeatTTL = 90 * time.Second
	}
	return &c, nil
}

func LoadDevice(path string) (*Device, error) {
	var c Device
	if err := loadYAML(path, &c); err != nil {
		return nil, err
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
	if c.ProxyIdleTimeout == 0 {
		c.ProxyIdleTimeout = 30 * time.Second
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 128
	}
	if c.DNSCacheTTL == 0 {
		c.DNSCacheTTL = 30 * time.Second
	}
	return &c, nil
}

func loadYAML(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, out)
}
