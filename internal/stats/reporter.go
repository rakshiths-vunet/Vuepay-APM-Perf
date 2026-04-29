package stats

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type counters struct {
	success atomic.Int64
	error   atomic.Int64
}

type intervalErrorSamples map[string]map[string]int64

type Reporter struct {
	journeys map[string]*counters
	order    []string
	interval time.Duration
	mu       sync.RWMutex
	errMu    sync.Mutex
	errs     intervalErrorSamples
}

func NewReporter(journeyNames []string, interval time.Duration) *Reporter {
	journeys := make(map[string]*counters, len(journeyNames))
	for _, name := range journeyNames {
		journeys[name] = &counters{}
	}
	order := make([]string, len(journeyNames))
	copy(order, journeyNames)
	return &Reporter{journeys: journeys, order: order, interval: interval, errs: make(intervalErrorSamples, len(journeyNames))}
}

func (r *Reporter) RecordSuccess(journeyName string) {
	if c := r.get(journeyName); c != nil {
		c.success.Add(1)
	}
}

func (r *Reporter) RecordError(journeyName string, err error) {
	if c := r.get(journeyName); c != nil {
		c.error.Add(1)
	}
	r.recordErrorSample(journeyName, err)
}

func (r *Reporter) Start(ctx context.Context) {
	go r.reportLoop(ctx)
}

func (r *Reporter) reportLoop(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	lastTotal := int64(0)
	lastSuccess := int64(0)
	lastError := int64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			total := int64(0)
			successTotal := int64(0)
			errorTotal := int64(0)
			parts := make([]string, 0, len(r.journeys)+1)
			samples := r.snapshotErrorSamples()

			r.mu.RLock()
			for _, name := range r.order {
				c := r.journeys[name]
				s := c.success.Load()
				e := c.error.Load()
				successTotal += s
				errorTotal += e
				total += s + e
				part := fmt.Sprintf("%s: %d ok/%d err", name, s, e)
				if sample := topErrorSample(samples[name]); sample != "" {
					part += fmt.Sprintf(" sample_err=%q", sample)
				}
				parts = append(parts, part)
			}
			r.mu.RUnlock()

			tps := total - lastTotal
			okTPS := successTotal - lastSuccess
			errTPS := errorTotal - lastError
			lastTotal = total
			lastSuccess = successTotal
			lastError = errorTotal

			timestamp := time.Now().Format("2006-01-02 15:04:05")
			fmt.Printf("[%s] TPS: %d | ok_tps: %d | err_tps: %d | %s\n", timestamp, tps, okTPS, errTPS, strings.Join(parts, " | "))
		}
	}
}

func (r *Reporter) get(journeyName string) *counters {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.journeys[journeyName]
}

func (r *Reporter) recordErrorSample(journeyName string, err error) {
	if err == nil {
		return
	}

	msg := normalizeError(err.Error())
	if msg == "" {
		return
	}

	r.errMu.Lock()
	defer r.errMu.Unlock()

	if r.errs[journeyName] == nil {
		r.errs[journeyName] = make(map[string]int64)
	}
	r.errs[journeyName][msg]++
}

func (r *Reporter) snapshotErrorSamples() intervalErrorSamples {
	r.errMu.Lock()
	defer r.errMu.Unlock()

	snapshot := make(intervalErrorSamples, len(r.errs))
	for journeyName, messages := range r.errs {
		copied := make(map[string]int64, len(messages))
		for message, count := range messages {
			copied[message] = count
		}
		snapshot[journeyName] = copied
	}
	r.errs = make(intervalErrorSamples, len(r.order))
	return snapshot
}

func topErrorSample(samples map[string]int64) string {
	if len(samples) == 0 {
		return ""
	}

	type candidate struct {
		message string
		count   int64
	}

	all := make([]candidate, 0, len(samples))
	for message, count := range samples {
		all = append(all, candidate{message: message, count: count})
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].count == all[j].count {
			return all[i].message < all[j].message
		}
		return all[i].count > all[j].count
	})

	return all[0].message
}

func normalizeError(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 140 {
		return message[:137] + "..."
	}
	return message
}
