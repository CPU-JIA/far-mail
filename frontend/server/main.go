package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	defaultPort    = "8080"
	defaultWebRoot = "/srv"
)

type staticHandler struct {
	root            string
	publicAPIOrigin string
	fileSystem      http.Handler
}

func newStaticHandler(root, publicAPIOrigin string) (http.Handler, error) {
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	if cleanRoot == "." || cleanRoot == "" {
		return nil, errors.New("web root is required")
	}
	info, err := os.Stat(filepath.Join(cleanRoot, "index.html"))
	if err != nil {
		return nil, fmt.Errorf("frontend index: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("frontend index must be a file")
	}
	cleanAPIOrigin, err := normalizePublicAPIOrigin(publicAPIOrigin)
	if err != nil {
		return nil, err
	}
	return &staticHandler{
		root:            cleanRoot,
		publicAPIOrigin: cleanAPIOrigin,
		fileSystem:      http.FileServer(http.Dir(cleanRoot)),
	}, nil
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header())
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	requestPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	if requestPath == "/runtime-config.js" {
		h.serveRuntimeConfig(w)
		return
	}
	if isReservedPath(requestPath) {
		http.NotFound(w, r)
		return
	}

	if requestPath == "/" {
		h.serveIndex(w, r)
		return
	}

	relativePath := strings.TrimPrefix(requestPath, "/")
	info, err := fs.Stat(os.DirFS(h.root), relativePath)
	if err == nil && !info.IsDir() {
		h.serveStatic(w, r, requestPath)
		return
	}

	if acceptsHTML(r.Header.Get("Accept")) && path.Ext(requestPath) == "" {
		h.serveIndex(w, r)
		return
	}
	http.NotFound(w, r)
}

func (h *staticHandler) serveRuntimeConfig(w http.ResponseWriter) {
	payload, _ := json.Marshal(map[string]string{"apiOrigin": h.publicAPIOrigin})
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = fmt.Fprintf(w, "window.__FAR_MAIL_RUNTIME_CONFIG__=%s;\n", payload)
}

func (h *staticHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	http.ServeFile(w, r, filepath.Join(h.root, "index.html"))
}

func (h *staticHandler) serveStatic(w http.ResponseWriter, r *http.Request, requestPath string) {
	w.Header().Set("Cache-Control", "public, max-age=604800")
	if strings.HasPrefix(requestPath, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	filePath := filepath.Join(h.root, filepath.FromSlash(strings.TrimPrefix(requestPath, "/")))
	if acceptsGzip(r.Header.Get("Accept-Encoding")) {
		if info, err := os.Stat(filePath + ".gz"); err == nil && !info.IsDir() {
			if contentType := mime.TypeByExtension(path.Ext(requestPath)); contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Vary", "Accept-Encoding")
			http.ServeFile(w, r, filePath+".gz")
			return
		}
	}
	h.fileSystem.ServeHTTP(w, r)
}

func setSecurityHeaders(header http.Header) {
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "SAMEORIGIN")
	header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
}

func isReservedPath(value string) bool {
	for _, prefix := range []string{
		"/console", "/api", "/public", "/health", "/internal",
		"/v1", "/auth", "/register", "/accounts",
	} {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return true
		}
	}
	return false
}

func acceptsHTML(value string) bool {
	return strings.Contains(strings.ToLower(value), "text/html")
}

func acceptsGzip(value string) bool {
	for _, item := range strings.Split(strings.ToLower(value), ",") {
		encoding := strings.TrimSpace(strings.SplitN(item, ";", 2)[0])
		if encoding == "gzip" || encoding == "*" {
			return true
		}
	}
	return false
}

func normalizePublicAPIOrigin(value string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("PUBLIC_API_ORIGIN must be an absolute HTTP(S) origin or empty")
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("PUBLIC_API_ORIGIN must not contain credentials, a path, query, or fragment")
	}
	return trimmed, nil
}

func main() {
	root := strings.TrimSpace(os.Getenv("WEB_ROOT"))
	if root == "" {
		root = defaultWebRoot
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = defaultPort
	}

	handler, err := newStaticHandler(root, os.Getenv("PUBLIC_API_ORIGIN"))
	if err != nil {
		log.Fatalf("frontend server configuration: %v", err)
	}
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-shutdownContext.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("frontend server shutdown: %v", err)
		}
	}()

	log.Printf("frontend static server listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("frontend server: %v", err)
	}
}
