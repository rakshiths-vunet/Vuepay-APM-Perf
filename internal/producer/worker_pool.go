package producer

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/your-org/vuepay-producer/internal/auth"
	"github.com/your-org/vuepay-producer/internal/config"
	"github.com/your-org/vuepay-producer/internal/journey"
	"github.com/your-org/vuepay-producer/internal/stats"
)

type WorkerPool struct {
	cfg        *config.Config
	tokens     *auth.TokenManager
	dispatcher *Dispatcher
	reporter   *stats.Reporter
	logger     *zap.Logger
	limiter    *rate.Limiter
	wg         sync.WaitGroup
}

func NewWorkerPool(cfg *config.Config, tokens *auth.TokenManager, dispatcher *Dispatcher, reporter *stats.Reporter, logger *zap.Logger) *WorkerPool {
	return &WorkerPool{
		cfg:        cfg,
		tokens:     tokens,
		dispatcher: dispatcher,
		reporter:   reporter,
		logger:     logger,
		limiter:    rate.NewLimiter(rate.Limit(cfg.Producer.TargetTPS), cfg.Producer.TargetTPS),
	}
}

func (p *WorkerPool) Start(ctx context.Context) {
	for i := 0; i < p.cfg.Producer.WorkerCount; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}
}

func (p *WorkerPool) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.wg.Wait()
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (p *WorkerPool) worker(ctx context.Context, id int) {
	defer p.wg.Done()
	workerLogger := p.logger.With(zap.Int("worker_id", id))

	for {
		if err := p.limiter.Wait(ctx); err != nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		j := p.dispatcher.Next()
		token := p.tokens.GetToken()
		if token == "" {
			time.Sleep(20 * time.Millisecond)
			continue
		}

		err := j.Execute(ctx, token)
		if errors.Is(err, journey.ErrUnauthorized) {
			p.tokens.RefreshNow()
			time.Sleep(20 * time.Millisecond)
			err = j.Execute(ctx, p.tokens.GetToken())
		}

		if err != nil {
			p.reporter.RecordError(j.Name())
			workerLogger.Debug("journey failed", zap.String("journey", j.Name()), zap.Error(err))
			continue
		}

		p.reporter.RecordSuccess(j.Name())
	}
}
