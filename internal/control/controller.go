package control

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/your-org/vuepay-producer/internal/auth"
	"github.com/your-org/vuepay-producer/internal/config"
	"github.com/your-org/vuepay-producer/internal/journey"
	"github.com/your-org/vuepay-producer/internal/producer"
	"github.com/your-org/vuepay-producer/internal/stats"
)

type Status struct {
	Running   bool   `json:"running"`
	Starting  bool   `json:"starting"`
	StartedAt string `json:"started_at"`
	Config    struct {
		TargetTPS     int `json:"target_tps"`
		WorkerCount   int `json:"worker_count"`
		HTTPTimeoutMs int `json:"http_timeout_ms"`
	} `json:"config"`
}

type Controller struct {
	parentCtx context.Context
	logger    *zap.Logger

	mu        sync.Mutex
	cfg       *config.Config
	running   bool
	starting  bool
	startedAt time.Time
	cancel    context.CancelFunc
	pool      *producer.WorkerPool
}

func NewController(parentCtx context.Context, cfg *config.Config, logger *zap.Logger) *Controller {
	return &Controller{
		parentCtx: parentCtx,
		cfg:       config.Clone(cfg),
		logger:    logger,
	}
}

func (c *Controller) Start() error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("already running")
	}
	if c.starting {
		c.mu.Unlock()
		return fmt.Errorf("already starting")
	}
	c.starting = true
	cfg := config.Clone(c.cfg)
	c.mu.Unlock()

	pool, cancel, err := c.buildPipeline(cfg)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.starting = false
	if err != nil {
		return err
	}
	if c.running {
		cancel()
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer waitCancel()
		pool.Wait(waitCtx)
		return fmt.Errorf("already running")
	}

	c.cancel = cancel
	c.pool = pool
	c.running = true
	c.startedAt = time.Now()

	c.logger.Info("producer started",
		zap.Int("workers", cfg.Producer.WorkerCount),
		zap.Int("target_tps", cfg.Producer.TargetTPS),
	)
	if timeoutLimitedTPS := maxTPSAtTimeout(cfg.Producer.WorkerCount, cfg.Producer.HTTPTimeoutMs); timeoutLimitedTPS < cfg.Producer.TargetTPS {
		c.logger.Warn("worker pool may cap throughput under slow responses",
			zap.Int("target_tps", cfg.Producer.TargetTPS),
			zap.Int("worker_count", cfg.Producer.WorkerCount),
			zap.Int("http_timeout_ms", cfg.Producer.HTTPTimeoutMs),
			zap.Int("timeout_limited_tps", timeoutLimitedTPS),
		)
	}

	return nil
}

func (c *Controller) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

func (c *Controller) Restart() error {
	c.Stop()
	return c.Start()
}

func (c *Controller) Configure(next *config.Config, restart bool, start bool) error {
	if next == nil {
		return fmt.Errorf("config payload is required")
	}
	cloned := config.Clone(next)
	if err := config.Prepare(cloned); err != nil {
		return err
	}

	c.mu.Lock()
	running := c.running
	c.cfg = cloned
	c.mu.Unlock()

	if restart && running {
		c.Stop()
		return c.Start()
	}
	if start && !running {
		return c.Start()
	}
	return nil
}

func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()

	var out Status
	out.Running = c.running
	out.Starting = c.starting
	if c.startedAt.IsZero() {
		out.StartedAt = formatIST(time.Now())
	} else {
		out.StartedAt = formatIST(c.startedAt)
	}
	out.Config.TargetTPS = c.cfg.Producer.TargetTPS
	out.Config.WorkerCount = c.cfg.Producer.WorkerCount
	out.Config.HTTPTimeoutMs = c.cfg.Producer.HTTPTimeoutMs
	return out
}

func formatIST(t time.Time) string {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return t.UTC().Format(time.RFC3339)
	}
	return t.In(loc).Format(time.RFC3339)
}

func (c *Controller) CurrentConfig() *config.Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return config.Clone(c.cfg)
}

func (c *Controller) buildPipeline(cfg *config.Config) (*producer.WorkerPool, context.CancelFunc, error) {
	httpClient := newHTTPClient(cfg)
	tokenManager := auth.NewTokenManager(cfg, httpClient, c.logger)
	if err := tokenManager.Start(c.parentCtx); err != nil {
		return nil, nil, fmt.Errorf("initial auth failed: %w", err)
	}

	journeys := []journey.Journey{
		journey.NewAccountJourney(cfg, httpClient),
		journey.NewPaymentJourney(cfg, httpClient),
		journey.NewBillsJourney(cfg, httpClient),
	}

	dispatcher, err := producer.NewDispatcher(journeys, cfg.Producer.JourneyWeights)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize dispatcher: %w", err)
	}

	reporter := stats.NewReporter([]string{"account", "payment_initiate", "bills"}, time.Duration(cfg.Producer.StatsIntervalSeconds)*time.Second)
	ctx, cancel := context.WithCancel(c.parentCtx)
	reporter.Start(ctx)

	pool := producer.NewWorkerPool(cfg, tokenManager, dispatcher, reporter, c.logger)
	pool.Start(ctx)
	return pool, cancel, nil
}

func (c *Controller) stopLocked() {
	if !c.running {
		return
	}

	cancel := c.cancel
	pool := c.pool
	c.cancel = nil
	c.pool = nil
	c.running = false
	c.startedAt = time.Time{}

	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	pool.Wait(waitCtx)

	c.logger.Info("producer stopped")
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

func maxTPSAtTimeout(workerCount int, httpTimeoutMs int) int {
	if workerCount <= 0 || httpTimeoutMs <= 0 {
		return 0
	}
	return int(float64(workerCount) / (float64(httpTimeoutMs) / 1000.0))
}

