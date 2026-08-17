package middleware

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestErrorEnvelopeDoesNotBufferSuccessfulSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	connectionClosed := make(chan struct{})
	router := gin.New()
	router.Use(ErrorEnvelope())
	router.GET("/events", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Status(http.StatusOK)
		_, _ = c.Writer.Write([]byte("event: ready\ndata: {}\n\n"))
		c.Writer.Flush()
		<-c.Request.Context().Done()
		close(connectionClosed)
	})

	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "event: ready\n" {
		t.Fatalf("SSE was not flushed immediately: line=%q err=%v", line, err)
	}

	cancel()
	select {
	case <-connectionClosed:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not observe client cancellation")
	}
}

func TestErrorEnvelopeNormalizesErrorResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID(), ErrorEnvelope())
	router.GET("/", func(c *gin.Context) { c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input"}) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	body := response.Body.String()
	for _, field := range []string{`"success":false`, `"error":"invalid input"`, `"error_code":"bad_request"`, `"request_id":"`} {
		if !contains(body, field) {
			t.Fatalf("error envelope missing %s: %s", field, body)
		}
	}
}

func TestErrorEnvelopeHidesInternalErrorDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorEnvelope())
	router.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "postgres password=do-not-leak"})
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	body := response.Body.String()
	if contains(body, "do-not-leak") || !contains(body, `"error":"internal server error"`) {
		t.Fatalf("internal error details leaked: %s", body)
	}
}

func contains(value, fragment string) bool {
	for i := 0; i+len(fragment) <= len(value); i++ {
		if value[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
