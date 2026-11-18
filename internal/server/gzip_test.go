package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGzipHandlerCompressesResponse(t *testing.T) {
	s := &Server{}
	payload := strings.Repeat("{\"message\":\"payload\"},", 500)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/workspaces/x/messages", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	s.gzipHandler(next).ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary = %q, want Accept-Encoding", got)
	}
	reader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != payload {
		t.Fatalf("decompressed body mismatch: %d bytes", len(body))
	}
}

func TestGzipHandlerSkipsEventStreams(t *testing.T) {
	s := &Server{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {}\n\n")
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/workspaces/x/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	s.gzipHandler(next).ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none for SSE", got)
	}
	if rec.Body.String() != "data: {}\n\n" {
		t.Fatalf("SSE body was altered: %q", rec.Body.String())
	}
}

func TestGzipHandlerSkipsWhenClientOptsOut(t *testing.T) {
	cases := []string{"", "identity", "gzip;q=0", "deflate, gzip;q=0.0"}
	s := &Server{}
	for _, encoding := range cases {
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "plain")
		})
		req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
		if encoding != "" {
			req.Header.Set("Accept-Encoding", encoding)
		}
		rec := httptest.NewRecorder()
		s.gzipHandler(next).ServeHTTP(rec, req)
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("Accept-Encoding %q: Content-Encoding = %q, want none", encoding, got)
		}
		if rec.Body.String() != "plain" {
			t.Fatalf("Accept-Encoding %q: body altered", encoding)
		}
	}
}
