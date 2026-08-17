package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type responseCaptureWriter struct {
	gin.ResponseWriter
	body        *bytes.Buffer
	statusCode  int
	wroteHeader bool
	passthrough bool
}

func newResponseCaptureWriter(w gin.ResponseWriter) *responseCaptureWriter {
	return &responseCaptureWriter{
		ResponseWriter: w,
		body:           bytes.NewBuffer(nil),
		statusCode:     http.StatusOK,
	}
}

func (w *responseCaptureWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.statusCode = code
	w.wroteHeader = true
	if code < http.StatusBadRequest {
		w.passthrough = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *responseCaptureWriter) WriteHeaderNow() {
	if !w.wroteHeader {
		w.WriteHeader(w.statusCode)
	}
}

func (w *responseCaptureWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(w.statusCode)
	}
	if w.passthrough {
		return w.ResponseWriter.Write(data)
	}
	return w.body.Write(data)
}

func (w *responseCaptureWriter) WriteString(s string) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(w.statusCode)
	}
	if w.passthrough {
		return w.ResponseWriter.WriteString(s)
	}
	return w.body.WriteString(s)
}

func (w *responseCaptureWriter) Status() int { return w.statusCode }

func (w *responseCaptureWriter) Written() bool { return w.wroteHeader }

func (w *responseCaptureWriter) Size() int {
	if w.passthrough {
		return w.ResponseWriter.Size()
	}
	return w.body.Len()
}

func ErrorEnvelope() gin.HandlerFunc {
	return func(c *gin.Context) {
		if shouldBypassEnvelope(c.Request.URL.Path) {
			c.Next()
			return
		}

		originalWriter := c.Writer
		capture := newResponseCaptureWriter(originalWriter)
		c.Writer = capture
		c.Next()
		if capture.passthrough {
			return
		}

		status := capture.statusCode
		if status == 0 {
			status = http.StatusOK
		}

		bodyBytes := capture.body.Bytes()
		if status >= http.StatusBadRequest && len(bodyBytes) > 0 {
			if normalized, ok := normalizeErrorBody(bodyBytes, status, GetRequestID(c)); ok {
				bodyBytes = normalized
				capture.Header().Set("Content-Type", "application/json; charset=utf-8")
			}
		}

		capture.Header().Del("Content-Length")
		originalWriter.WriteHeader(status)
		if len(bodyBytes) > 0 {
			_, _ = originalWriter.Write(bodyBytes)
		}
	}
}

func normalizeErrorBody(body []byte, status int, requestID string) ([]byte, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}

	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, false
	}

	message := pickErrorMessage(payload)
	if message == "" {
		message = http.StatusText(status)
	}
	if status >= http.StatusInternalServerError {
		// Database, filesystem and provider errors can contain connection strings,
		// SQL details or upstream response fragments. Keep diagnostics in server
		// logs and expose only a stable public message plus the request ID.
		message = "internal server error"
		payload["error_code"] = "internal_error"
	}

	payload["success"] = false
	payload["status"] = status
	payload["request_id"] = requestID
	payload["message"] = message
	payload["error"] = message
	if _, exists := payload["error_code"]; !exists {
		payload["error_code"] = inferErrorCode(status)
	}

	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	return normalized, true
}

func pickErrorMessage(payload map[string]any) string {
	if value, ok := payload["message"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if value, ok := payload["error"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return ""
}

func inferErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		if status >= http.StatusInternalServerError {
			return "internal_error"
		}
		return "request_failed"
	}
}

func shouldBypassEnvelope(path string) bool {
	return false
}
