package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Server struct {
	SocksListen   string        `yaml:"socks_listen"`
	DeviceListen  string        `yaml:"device_listen"`
	TLSCert       string        `yaml:"tls_cert"`
	TLSKey        string        `yaml:"tls_key"`
	SocksPassword string        `yaml:"socks_password"`
	DeviceWait    time.Duration `yaml:"device_wait"`
	ConnectWait      time.Duration `yaml:"connect_wait"`
	HeartbeatTTL     time.Duration `yaml:"heartbeat_ttl"`
	ProxyIdleTimeout time.Duration `yaml:"proxy_idle_timeout"`
}

type Device struct {
	DeviceID          string        `yaml:"device_id"`
	ServerAddr        string        `yaml:"server_addr"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	ReconnectMin      time.Duration `yaml:"reconnect_min"`
	ReconnectMax      time.Duration `yaml:"reconnect_max"`
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
		c.ProxyIdleTimeout = 2 * time.Minute
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
	return &c, nil
}

func loadYAML(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, out)
}
