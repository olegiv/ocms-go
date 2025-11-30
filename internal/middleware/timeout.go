package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Timeout wraps an http.Handler and applies a request timeout.
// If the handler doesn't complete within the timeout duration,
// a 503 Service Unavailable response is sent.
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			// Channel to signal completion
			done := make(chan struct{})

			// Wrap the response writer to detect if headers have been written
			tw := &timeoutWriter{
				ResponseWriter: w,
			}

			go func() {
				next.ServeHTTP(tw, r.WithContext(ctx))
				close(done)
			}()

			select {
			case <-done:
				// Handler completed normally
				return
			case <-ctx.Done():
				// Timeout occurred
				tw.mu.Lock()
				defer tw.mu.Unlock()
				if !tw.wroteHeader {
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.WriteHeader(http.StatusServiceUnavailable)
					w.Write([]byte("Request timeout"))
				}
			}
		})
	}
}

// timeoutWriter wraps http.ResponseWriter to track if headers have been written.
type timeoutWriter struct {
	http.ResponseWriter
	mu          sync.Mutex
	wroteHeader bool
}

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if !tw.wroteHeader {
		tw.wroteHeader = true
		tw.ResponseWriter.WriteHeader(code)
	}
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if !tw.wroteHeader {
		tw.wroteHeader = true
		tw.ResponseWriter.WriteHeader(http.StatusOK)
	}
	return tw.ResponseWriter.Write(b)
}
