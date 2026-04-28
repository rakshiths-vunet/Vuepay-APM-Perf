package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/your-org/vuepay-producer/internal/auth"
	"github.com/your-org/vuepay-producer/internal/config"
	"github.com/your-org/vuepay-producer/internal/journey"
	"github.com/your-org/vuepay-producer/internal/producer"
	"github.com/your-org/vuepay-producer/internal/stats"
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

	httpClient := newHTTPClient(cfg)
	tokenManager := auth.NewTokenManager(cfg, httpClient, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	defer cancel()

	if err := tokenManager.Start(ctx); err != nil {
		logger.Fatal("initial auth failed", zap.Error(err))
	}

	journeys := []journey.Journey{
		journey.NewAccountJourney(cfg, httpClient),
		journey.NewPaymentJourney(cfg, httpClient),
		journey.NewBillsJourney(cfg, httpClient),
	}

	dispatcher, err := producer.NewDispatcher(journeys, cfg.Producer.JourneyWeights)
	if err != nil {
		logger.Fatal("failed to initialize dispatcher", zap.Error(err))
	}

	reporter := stats.NewReporter([]string{"account", "payment_initiate", "bills"}, time.Duration(cfg.Producer.StatsIntervalSeconds)*time.Second)
	reporter.Start(ctx)

	pool := producer.NewWorkerPool(cfg, tokenManager, dispatcher, reporter, logger)
	pool.Start(ctx)

	logger.Info("producer started",
		zap.Int("workers", cfg.Producer.WorkerCount),
		zap.Int("target_tps", cfg.Producer.TargetTPS),
	)

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	logger.Info("shutting down")
	pool.Wait(shutdownCtx)
}

func newHTTPClient(cfg *config.Config) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          1000,
		MaxIdleConnsPerHost:   1000,
		MaxConnsPerHost:       1000,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: cfg.Server.TLSSkipVerify},
		DisableKeepAlives:     false,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   time.Duration(cfg.Producer.HTTPTimeoutMs) * time.Millisecond,
	}
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
