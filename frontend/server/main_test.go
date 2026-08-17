package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>console</main>"), 0o644); err != nil {
		t.Fatal(err)
	}
	asset := []byte("console.log('far-mail')")
	assetPath := filepath.Join(root, "assets", "app-123.js")
	if err := os.WriteFile(assetPath, asset, 0o644); err != nil {
		t.Fatal(err)
	}
	gzipFile, err := os.Create(assetPath + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	writer := gzip.NewWriter(gzipFile)
	if _, err := writer.Write(asset); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipFile.Close(); err != nil {
		t.Fatal(err)
	}
	handler, err := newStaticHandler(root, "")
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestStaticHandlerRuntimeConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler, err := newStaticHandler(root, "https://mail.example.test/")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/runtime-config.js", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"apiOrigin":"https://mail.example.test"`) {
		t.Fatalf("runtime config: status=%d body=%q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("runtime config cache policy = %q", response.Header().Get("Cache-Control"))
	}

	if _, err := newStaticHandler(root, "javascript:alert(1)"); err == nil {
		t.Fatal("invalid public API origin must be rejected")
	}
}

func TestStaticHandlerSPAFallbackAndCache(t *testing.T) {
	handler := testHandler(t)

	request := httptest.NewRequest(http.MethodGet, "/settings", nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "console") {
		t.Fatalf("SPA fallback: status=%d body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("index cache policy = %q", got)
	}

	request = httptest.NewRequest(http.MethodGet, "/assets/app-123.js", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("asset status = %d", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("asset cache policy = %q", got)
	}
}

func TestStaticHandlerServesPrecompressedAssets(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/assets/app-123.js", nil)
	request.Header.Set("Accept-Encoding", "br, gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip response: status=%d encoding=%q", response.Code, response.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "console.log('far-mail')" {
		t.Fatalf("unexpected gzip content %q", content)
	}
}

func TestStaticHandlerRejectsAPIRoutesAndUnsafeMethods(t *testing.T) {
	handler := testHandler(t)
	for _, route := range []string{
		"/console/v1/session",
		"/api/v1/domains",
		"/public/v1/settings",
		"/health",
		"/internal/domains.txt",
		"/v1/mailboxes",
		"/auth/login",
		"/register",
		"/accounts",
		"/assets/missing.js",
	} {
		request := httptest.NewRequest(http.MethodGet, route, nil)
		request.Header.Set("Accept", "text/html")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d", route, response.Code)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST /: status=%d allow=%q", response.Code, response.Header().Get("Allow"))
	}
}
