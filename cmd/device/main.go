package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"xproxy/internal/config"
	"xproxy/internal/device"
)

func main() {
	cfgPath := flag.String("config", "configs/device.yaml", "config file")
	flag.Parse()

	cfg, err := config.LoadDevice(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}

	logger := log.New(os.Stdout, "device ", log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := device.Run(ctx, cfg, logger); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}
