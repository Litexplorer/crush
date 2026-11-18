package server

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// gzipHandler compresses responses when the client accepts gzip. Event
// streams are skipped: wrapping per-event flushes in a gzip writer adds
// latency, and the SSE proxy path expects each chunk to reach the browser as
// it is produced.
func (s *Server) gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) || isEventStreamPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		// The compressed length is unknown up front; let the server chunk it.
		w.Header().Del("Content-Length")
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer func() { _ = gw.Close() }()
		next.ServeHTTP(gw, r)
	})
}

// acceptsGzip reports whether the request's Accept-Encoding asks for gzip,
// honouring q=0 as a refusal.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		fields := strings.Split(part, ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			continue
		}
		for _, param := range fields[1:] {
			if strings.EqualFold(strings.TrimSpace(param), "q=0") ||
				strings.EqualFold(strings.TrimSpace(param), "q=0.0") ||
				strings.EqualFold(strings.TrimSpace(param), "q=0.00") ||
				strings.EqualFold(strings.TrimSpace(param), "q=0.000") {
				return false
			}
		}
		return true
	}
	return false
}

// isEventStreamPath keeps the SSE endpoint out of the compressor.
func isEventStreamPath(path string) bool {
	return strings.HasSuffix(path, "/events")
}

// gzipResponseWriter starts compressing on the first write so handlers that
// only set headers (or write nothing) pay no cost.
type gzipResponseWriter struct {
	http.ResponseWriter
	writer  *gzip.Writer
	started bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	g.start()
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	g.start()
	return g.writer.Write(p)
}

// Flush is required by http.Flusher; some handlers check for it before
// streaming.
func (g *gzipResponseWriter) Flush() {
	g.start()
	_ = g.writer.Flush()
	if flusher, ok := g.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Close ends the gzip stream; without it the response is truncated.
func (g *gzipResponseWriter) Close() error {
	if g.writer == nil {
		return nil
	}
	return g.writer.Close()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return g.ResponseWriter
}

func (g *gzipResponseWriter) start() {
	if g.started {
		return
	}
	g.started = true
	g.Header().Set("Content-Encoding", "gzip")
	g.writer = gzip.NewWriter(g.ResponseWriter)
}
