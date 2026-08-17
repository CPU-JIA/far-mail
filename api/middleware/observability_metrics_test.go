package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"farmail/model"
	"farmail/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestAPIUsageRecorderQueueCountersWithoutDatabase(t *testing.T) {
	recorder := &APIUsageRecorder{queue: make(chan store.APIRequestEvent, 1)}
	router := gin.New()
	router.Use(recorder.Middleware())
	router.GET("/api/v1/test", func(c *gin.Context) {
		c.Set(TokenKey, &model.AccountToken{ID: uuid.New()})
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	router.ServeHTTP(httptest.NewRecorder(), request)
	router.ServeHTTP(httptest.NewRecorder(), request)
	stats := recorder.Stats()
	if stats.Enqueued != 1 || stats.Dropped != 1 || stats.QueueDepth != 1 || stats.QueueHighWater != 1 {
		t.Fatalf("unexpected observability stats: %+v", stats)
	}
}

type retryingAPIUsageStore struct {
	mu             sync.Mutex
	remainingFails int
	stored         []store.APIRequestEvent
}

func (s *retryingAPIUsageStore) RecordAPIRequestEvents(_ context.Context, events []store.APIRequestEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remainingFails > 0 {
		s.remainingFails--
		return errors.New("temporary database outage")
	}
	s.stored = append(s.stored, events...)
	return nil
}

func (*retryingAPIUsageStore) RecordTokenUsage(context.Context, uuid.UUID) error { return nil }
func (*retryingAPIUsageStore) DeleteOldAPIRequestEvents(context.Context) error   { return nil }

func TestAPIUsageRecorderRetriesFailedBatchWithoutDuplication(t *testing.T) {
	fakeStore := &retryingAPIUsageStore{remainingFails: 1}
	recorder := newAPIUsageRecorder(fakeStore, 8, 10*time.Millisecond)
	defer recorder.Close()

	router := gin.New()
	router.Use(recorder.Middleware())
	tokenID := uuid.New()
	router.GET("/api/v1/retry", func(c *gin.Context) {
		c.Set(TokenKey, &model.AccountToken{ID: tokenID})
		c.Status(http.StatusNoContent)
	})
	for index := 0; index < 3; index++ {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/retry", nil))
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stats := recorder.Stats()
		if stats.FlushErrors >= 1 && stats.FlushedEvents == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stats := recorder.Stats()
	if stats.FlushErrors != 1 || stats.FlushedEvents != 3 || stats.FailedEvents != 0 || stats.PendingDepth != 0 {
		t.Fatalf("unexpected retry stats: %+v", stats)
	}
	fakeStore.mu.Lock()
	defer fakeStore.mu.Unlock()
	if len(fakeStore.stored) != 3 {
		t.Fatalf("stored events = %d, want 3", len(fakeStore.stored))
	}
	for _, event := range fakeStore.stored {
		if event.TokenID != tokenID || event.Route != "/api/v1/retry" {
			t.Fatalf("unexpected stored event: %+v", event)
		}
	}
}
