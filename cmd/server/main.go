package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"xproxy/internal/config"
	"xproxy/internal/socks"
	"xproxy/internal/tunnel"
)

func main() {
	cfgPath := flag.String("config", "configs/server.yaml", "config file")
	flag.Parse()

	cfg, err := config.LoadServer(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}

	logger := log.New(os.Stdout, "server ", log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	reg := tunnel.NewRegistry()

	cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		log.Fatal(err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	devLn, err := tls.Listen("tcp", cfg.DeviceListen, tlsCfg)
	if err != nil {
		log.Fatal(err)
	}

	socksSrv := socks.New(cfg, reg, logger)
	if err := socksSrv.Listen(); err != nil {
		log.Fatal(err)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := devLn.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				logger.Printf("device accept: %v", err)
				return
			}
			wg.Add(1)
			go func(conn net.Conn) {
				defer wg.Done()
				tunnel.ServeDevice(conn, reg, cfg.HeartbeatTTL, logger)
			}(c)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := socksSrv.Serve(); err != nil && !errors.Is(err, net.ErrClosed) {
			logger.Printf("socks: %v", err)
		}
	}()

	logger.Printf("socks=%s device_tls=%s", cfg.SocksListen, cfg.DeviceListen)

	<-ctx.Done()
	_ = devLn.Close()
	_ = socksSrv.Close()
	reg.CloseAll()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}
