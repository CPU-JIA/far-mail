package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestIDRejectsUnsafeOrOversizedValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, strings.Repeat("x", 65))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	requestID := response.Header().Get(RequestIDHeader)
	if requestID == strings.Repeat("x", 65) || !validRequestID(requestID) {
		t.Fatalf("unsafe request id was not replaced: %q", requestID)
	}
}

func TestRequestIDPreservesSafeValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, "trace-01.a_b:99")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if got := response.Header().Get(RequestIDHeader); got != "trace-01.a_b:99" {
		t.Fatalf("safe request id changed: %q", got)
	}
}
