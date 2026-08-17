package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"farmail/model"

	"github.com/gin-gonic/gin"
)

func TestSetTokenQuotaHeadersIncludesEveryQuotaDimension(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	now := time.Date(2026, 7, 30, 9, 10, 15, 0, time.UTC)
	token := &model.AccountToken{
		RateLimitPerMinute: 30,
		DailyRequestLimit:  5000,
		TotalRequestLimit:  100000,
	}
	setTokenQuotaHeaders(ctx, token, tokenQuotaState{
		MinuteRemaining: 22,
		MinuteReset:     now.Add(45 * time.Second),
		DailyRemaining:  4100,
		DailyReset:      now.Add(6 * time.Hour),
		TotalRemaining:  88000,
	}, now)

	expected := map[string]string{
		"RateLimit-Limit":              "30",
		"RateLimit-Remaining":          "22",
		"RateLimit-Reset":              "45",
		"RateLimit-Policy":             "30;w=60, 5000;w=21600",
		"X-RateLimit-Minute-Limit":     "30",
		"X-RateLimit-Minute-Remaining": "22",
		"X-RateLimit-Daily-Limit":      "5000",
		"X-RateLimit-Daily-Remaining":  "4100",
		"X-RateLimit-Total-Limit":      "100000",
		"X-RateLimit-Total-Remaining":  "88000",
	}
	for name, value := range expected {
		if got := recorder.Header().Get(name); got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestSetTokenQuotaHeadersOmitsUnlimitedDimensions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	setTokenQuotaHeaders(ctx, &model.AccountToken{}, tokenQuotaState{}, time.Now())
	if got := recorder.Header().Get("RateLimit-Limit"); got != "" {
		t.Fatalf("unlimited token must not advertise a finite limit, got %q", got)
	}
}
