package stats

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type counters struct {
	success atomic.Int64
	error   atomic.Int64
}

type Reporter struct {
	journeys map[string]*counters
	order    []string
	interval time.Duration
	mu       sync.RWMutex
}

func NewReporter(journeyNames []string, interval time.Duration) *Reporter {
	journeys := make(map[string]*counters, len(journeyNames))
	for _, name := range journeyNames {
		journeys[name] = &counters{}
	}
	order := make([]string, len(journeyNames))
	copy(order, journeyNames)
	return &Reporter{journeys: journeys, order: order, interval: interval}
}

func (r *Reporter) RecordSuccess(journeyName string) {
	if c := r.get(journeyName); c != nil {
		c.success.Add(1)
	}
}

func (r *Reporter) RecordError(journeyName string) {
	if c := r.get(journeyName); c != nil {
		c.error.Add(1)
	}
}

func (r *Reporter) Start(ctx context.Context) {
	go r.reportLoop(ctx)
}

func (r *Reporter) reportLoop(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	lastTotal := int64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			total := int64(0)
			parts := make([]string, 0, len(r.journeys)+1)

			r.mu.RLock()
			for _, name := range r.order {
				c := r.journeys[name]
				s := c.success.Load()
				e := c.error.Load()
				total += s + e
				parts = append(parts, fmt.Sprintf("%s: %d ok/%d err", name, s, e))
			}
			r.mu.RUnlock()

			tps := total - lastTotal
			lastTotal = total

			timestamp := time.Now().Format("2006-01-02 15:04:05")
			fmt.Printf("[%s] TPS: %d | %s\n", timestamp, tps, strings.Join(parts, " | "))
		}
	}
}

func (r *Reporter) get(journeyName string) *counters {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.journeys[journeyName]
}
