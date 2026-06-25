package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/uberswe/tesseract/internal/config"
	"github.com/uberswe/tesseract/internal/db"
	"github.com/uberswe/tesseract/internal/health"
	"github.com/uberswe/tesseract/internal/inventory"
	"github.com/uberswe/tesseract/internal/server"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	pool, err := db.NewPostgres(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	store := inventory.NewStore()
	broadcaster := inventory.NewBroadcaster()
	persister := db.NewPersister(pool, store)

	healthSrv := health.NewServer(cfg.HealthPort)
	healthSrv.Start()

	persistCtx, persistCancel := context.WithCancel(context.Background())
	go persister.Run(persistCtx, time.Duration(cfg.PersistIntervalSec)*time.Second)

	tcpServer := server.New(store, broadcaster, persister, cfg.MaxPacketSize, time.Duration(cfg.PingIntervalSec)*time.Second)

	healthSrv.SetReady(true)

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := tcpServer.Listen(context.Background(), ":"+cfg.TCPPort); err != nil {
			slog.Error("TCP server error", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("tesseract-service started", "tcp_port", cfg.TCPPort, "health_port", cfg.HealthPort)

	<-done
	slog.Info("shutting down...")

	healthSrv.SetReady(false)
	tcpServer.Close()
	persistCancel()

	slog.Info("flushing all inventories to database...")
	persister.SaveAll(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	healthSrv.Shutdown(ctx)

	slog.Info("tesseract-service stopped")
}
