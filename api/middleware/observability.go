package middleware

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"farmail/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type APIUsageRecorder struct {
	store          apiUsageStore
	queue          chan store.APIRequestEvent
	stop           chan struct{}
	done           chan struct{}
	closeOnce      sync.Once
	enqueued       atomic.Uint64
	dropped        atomic.Uint64
	flushes        atomic.Uint64
	flushed        atomic.Uint64
	flushErrs      atomic.Uint64
	failedEvents   atomic.Uint64
	lastFlush      atomic.Int64
	queueHighWater atomic.Uint64
	pendingDepth   atomic.Int64
	flushInterval  time.Duration
}

type apiUsageStore interface {
	RecordAPIRequestEvents(context.Context, []store.APIRequestEvent) error
	RecordTokenUsage(context.Context, uuid.UUID) error
	DeleteOldAPIRequestEvents(context.Context) error
}

type APIUsageRecorderStats struct {
	QueueDepth       int    `json:"queue_depth"`
	QueueCapacity    int    `json:"queue_capacity"`
	PendingDepth     int64  `json:"pending_depth"`
	QueueHighWater   uint64 `json:"queue_high_water"`
	Enqueued         uint64 `json:"enqueued"`
	Dropped          uint64 `json:"dropped"`
	Flushes          uint64 `json:"flushes"`
	FlushedEvents    uint64 `json:"flushed_events"`
	FlushErrors      uint64 `json:"flush_errors"`
	FailedEvents     uint64 `json:"failed_events"`
	LastFlushUnixSec int64  `json:"last_flush_unix_sec,omitempty"`
}

func NewAPIUsageRecorder(s *store.Store) *APIUsageRecorder {
	return newAPIUsageRecorder(s, 4096, time.Second)
}

func newAPIUsageRecorder(s apiUsageStore, queueCapacity int, flushInterval time.Duration) *APIUsageRecorder {
	if queueCapacity < 1 {
		queueCapacity = 1
	}
	if flushInterval <= 0 {
		flushInterval = time.Second
	}
	recorder := &APIUsageRecorder{
		store:         s,
		queue:         make(chan store.APIRequestEvent, queueCapacity),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		flushInterval: flushInterval,
	}
	go recorder.run()
	return recorder
}

func (r *APIUsageRecorder) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		token := GetToken(c)
		if token == nil {
			return
		}
		route := c.FullPath()
		if route == "" {
			route = "/api/v1/unknown"
		}
		event := store.APIRequestEvent{
			TokenID:         token.ID,
			Method:          c.Request.Method,
			Route:           route,
			Status:          c.Writer.Status(),
			LatencyMS:       int(time.Since(started).Milliseconds()),
			RequestID:       strings.TrimSpace(c.Writer.Header().Get(RequestIDHeader)),
			CreatedAt:       time.Now(),
			CountTokenUsage: deferredTokenUsage(c),
		}
		select {
		case r.queue <- event:
			r.enqueued.Add(1)
			r.observeQueueHighWater()
		default:
			r.dropped.Add(1)
			// Preserve unlimited-token usage telemetry if the observability queue is
			// saturated. This slow fallback only runs under exceptional pressure.
			if event.CountTokenUsage {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				if err := r.store.RecordTokenUsage(ctx, event.TokenID); err != nil {
					log.Printf("[api-observability] usage fallback failed: %v", err)
				}
				cancel()
			}
		}
	}
}

func (r *APIUsageRecorder) Stats() APIUsageRecorderStats {
	return APIUsageRecorderStats{
		QueueDepth:       len(r.queue),
		QueueCapacity:    cap(r.queue),
		PendingDepth:     r.pendingDepth.Load(),
		QueueHighWater:   r.queueHighWater.Load(),
		Enqueued:         r.enqueued.Load(),
		Dropped:          r.dropped.Load(),
		Flushes:          r.flushes.Load(),
		FlushedEvents:    r.flushed.Load(),
		FlushErrors:      r.flushErrs.Load(),
		FailedEvents:     r.failedEvents.Load(),
		LastFlushUnixSec: r.lastFlush.Load(),
	}
}

func (r *APIUsageRecorder) observeQueueHighWater() {
	value := uint64(len(r.queue))
	for previous := r.queueHighWater.Load(); value > previous; previous = r.queueHighWater.Load() {
		if r.queueHighWater.CompareAndSwap(previous, value) {
			return
		}
	}
}

func deferredTokenUsage(c *gin.Context) bool {
	value, exists := c.Get(DeferredTokenUsageKey)
	if !exists {
		return false
	}
	deferred, _ := value.(bool)
	return deferred
}

func (r *APIUsageRecorder) Close() {
	r.closeOnce.Do(func() { close(r.stop); <-r.done })
}

func (r *APIUsageRecorder) run() {
	defer close(r.done)
	const batchSize = 256
	flushTicker := time.NewTicker(r.flushInterval)
	cleanupTicker := time.NewTicker(time.Hour)
	defer flushTicker.Stop()
	defer cleanupTicker.Stop()
	pending := make([]store.APIRequestEvent, 0, batchSize)
	var retryAfter time.Time
	flush := func(force bool) bool {
		if len(pending) == 0 {
			return true
		}
		if !force && time.Now().Before(retryAfter) {
			return false
		}
		count := len(pending)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := r.store.RecordAPIRequestEvents(ctx, pending); err != nil {
			r.flushErrs.Add(1)
			log.Printf("[api-observability] batch insert failed: %v", err)
			retryAfter = time.Now().Add(r.flushInterval)
		} else {
			r.flushed.Add(uint64(count))
			r.lastFlush.Store(time.Now().Unix())
			pending = pending[:0]
			r.pendingDepth.Store(0)
			retryAfter = time.Time{}
		}
		r.flushes.Add(1)
		cancel()
		return len(pending) == 0
	}
	for {
		var eventQueue <-chan store.APIRequestEvent = r.queue
		if len(pending) >= batchSize {
			// Keep the failed batch intact and let the bounded public queue apply
			// backpressure. This prevents both data loss and unbounded RAM growth.
			eventQueue = nil
		}
		select {
		case event := <-eventQueue:
			pending = append(pending, event)
			r.pendingDepth.Store(int64(len(pending)))
			if len(pending) >= batchSize {
				flush(false)
			}
		case <-flushTicker.C:
			flush(false)
		case <-cleanupTicker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := r.store.DeleteOldAPIRequestEvents(ctx); err != nil {
				log.Printf("[api-observability] retention cleanup failed: %v", err)
			}
			cancel()
		case <-r.stop:
			for {
				if len(pending) >= batchSize {
					if !flush(true) {
						r.failedEvents.Add(uint64(len(pending) + len(r.queue)))
						return
					}
				}
				select {
				case event := <-r.queue:
					pending = append(pending, event)
					r.pendingDepth.Store(int64(len(pending)))
				default:
					if !flush(true) {
						r.failedEvents.Add(uint64(len(pending)))
					}
					return
				}
			}
		}
	}
}
