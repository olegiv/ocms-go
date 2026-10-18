// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// Timeout wraps an http.Handler and applies a request timeout. If the
// handler has not started its response when the timeout expires, Timeout
// answers 503 Service Unavailable.
//
// The handler runs in a goroutine of its own, beyond the reach of any
// recovering middleware in front of Timeout, so Timeout recovers its panics
// itself: an unrecovered panic in that goroutine would end the process. The
// handler also sets its headers in a map of its own (see timeoutWriter), as
// a handler still setting headers when Timeout answers would otherwise
// write the map Timeout answers with, and concurrent map writes end the
// process too.
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			// Channel to signal completion
			done := make(chan struct{})

			tw := newTimeoutWriter(w)

			go func() {
				defer close(done)
				defer recoverHandlerPanic(tw, r)
				next.ServeHTTP(tw, r.WithContext(ctx))
			}()

			select {
			case <-done:
				// Handler completed normally, or panicked and was recovered.
				// An abort is passed on so net/http drops the connection.
				if tw.aborted() {
					panic(http.ErrAbortHandler)
				}
				return
			case <-ctx.Done():
				// Timeout occurred
				tw.mu.Lock()
				defer tw.mu.Unlock()
				tw.timedOut = true
				if !tw.wroteHeader {
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte("Request timeout"))
				}
			}
		})
	}
}

// recoverHandlerPanic recovers a panic of the handler Timeout runs. It logs
// the panic with its stack and, unless the request has timed out, settles
// the response: a 500 without the headers the handler set if no response was
// started, or else an abort, so net/http drops the connection rather than
// end the partial response as if it were complete. http.ErrAbortHandler,
// which aborts a response on purpose, is not logged: Timeout passes it on if
// the handler aborted before the timeout, and ignores it after.
func recoverHandlerPanic(tw *timeoutWriter, r *http.Request) {
	p := recover()
	if p == nil {
		return
	}
	err, _ := p.(error)
	abort := errors.Is(err, http.ErrAbortHandler)
	if !abort {
		slog.Error("panic serving request", "panic", p, "method", r.Method, "path", r.URL.Path,
			"request_id", chimw.GetReqID(r.Context()), "stack", string(debug.Stack()))
	}
	tw.mu.Lock()
	defer tw.mu.Unlock()
	switch {
	case tw.timedOut:
	case abort || tw.wroteHeader:
		tw.abort = true
	default:
		tw.wroteHeader = true
		tw.ResponseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
		tw.ResponseWriter.WriteHeader(http.StatusInternalServerError)
		_, _ = tw.ResponseWriter.Write([]byte(http.StatusText(http.StatusInternalServerError)))
	}
}

// timeoutWriter is the http.ResponseWriter of the handler Timeout runs. The
// handler's headers go to a map of its own, copied to the underlying writer
// when the response starts. Once the request has timed out, the handler's
// WriteHeader, Write and Flush calls are dropped: Timeout has returned by
// then, after answering 503 if no response was started.
type timeoutWriter struct {
	http.ResponseWriter
	header      http.Header
	mu          sync.Mutex
	wroteHeader bool
	timedOut    bool
	abort       bool
}

// newTimeoutWriter returns a timeoutWriter for w, starting from the headers
// set on w so far.
func newTimeoutWriter(w http.ResponseWriter) *timeoutWriter {
	return &timeoutWriter{ResponseWriter: w, header: w.Header().Clone()}
}

// Header returns the handler's header map.
func (tw *timeoutWriter) Header() http.Header {
	return tw.header
}

// aborted reports whether the handler's response is to be aborted.
func (tw *timeoutWriter) aborted() bool {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.abort
}

// startResponse copies the handler's headers to the underlying writer and
// writes the status code. tw.mu must be held.
func (tw *timeoutWriter) startResponse(code int) {
	tw.wroteHeader = true
	dst := tw.ResponseWriter.Header()
	clear(dst)
	maps.Copy(dst, tw.header.Clone())
	tw.ResponseWriter.WriteHeader(code)
}

func (tw *timeoutWriter) FlushError() error {
	tw.WriteHeader(http.StatusOK)
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		return http.ErrHandlerTimeout
	}
	return http.NewResponseController(tw.ResponseWriter).Flush()
}

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if !tw.wroteHeader && !tw.timedOut {
		tw.startResponse(code)
	}
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	if !tw.wroteHeader {
		// If no Content-Type set, default to plain text to prevent XSS
		if tw.header.Get("Content-Type") == "" {
			tw.header.Set("Content-Type", "text/plain; charset=utf-8")
		}
		tw.startResponse(http.StatusOK)
	}
	return tw.ResponseWriter.Write(b)
}
