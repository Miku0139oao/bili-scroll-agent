package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Miku0139oao/bili-scroll-agent/internal/app"
	"github.com/Miku0139oao/bili-scroll-agent/internal/config"
)

func main() {
	log.SetOutput(os.Stderr)
	settings, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, &settings)
	if err != nil {
		log.Fatal(err)
	}
	defer application.Close()
	application.Start(ctx)
	if err := application.RunLocalMCP(ctx, os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
