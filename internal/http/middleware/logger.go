package middleware

import (
	"log"
	"net/http"
	"time"

	"studdle/backend/internal/authctx"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer when it supports http.Flusher, so
// SSE handlers downstream can push chunks without buffering.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Logger emits one line per request with method, path, status, duration, uid.
func Logger() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sr := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(sr, r)
			log.Printf("%s %s -> %d (%s) uid=%d rid=%s",
				r.Method, r.URL.Path, sr.status, time.Since(start),
				authctx.UID(r.Context()), RequestIDFromContext(r.Context()))
		})
	}
}
