package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/your-org/vuepay-producer/internal/config"
	"github.com/your-org/vuepay-producer/internal/control"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to YAML config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger, cleanup, err := buildLogger(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to setup logger: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	defer cancel()
	controller := control.NewController(ctx, cfg, logger)

	var webhookServer *control.WebhookServer
	if cfg.Webhook.Enabled {
		webhookServer = control.NewWebhookServer(cfg, *configPath, controller, logger)
		webhookServer.Start()
	}

	if !cfg.Webhook.Enabled || cfg.Webhook.AutoStart {
		if err := controller.Start(); err != nil {
			logger.Fatal("failed to start producer", zap.Error(err))
		}
	}

	<-ctx.Done()
	controller.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if webhookServer != nil {
		webhookServer.Shutdown(shutdownCtx)
	}

	logger.Info("shutting down")
}

func buildLogger(cfg *config.Config) (*zap.Logger, func(), error) {
	level := zapcore.InfoLevel
	switch cfg.Logging.Level {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	if cfg.Logging.Format == "text" {
		encoder = zapcore.NewConsoleEncoder(encoderCfg)
	} else {
		encoder = zapcore.NewJSONEncoder(encoderCfg)
	}

	var ws zapcore.WriteSyncer
	cleanup := func() {}
	if cfg.Logging.Output == "" || cfg.Logging.Output == "stdout" {
		ws = zapcore.AddSync(os.Stdout)
	} else {
		f, err := os.OpenFile(cfg.Logging.Output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, err
		}
		cleanup = func() {
			_ = f.Close()
		}
		ws = zapcore.AddSync(f)
	}

	core := zapcore.NewCore(encoder, ws, level)
	return zap.New(core), cleanup, nil
}

