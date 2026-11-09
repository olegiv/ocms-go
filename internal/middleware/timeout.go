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
	"strconv"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// Timeout wraps an http.Handler and applies a request timeout. If the
// handler has not started its response when the timeout expires, Timeout
// answers 503 Service Unavailable. It aborts an incomplete response already
// in progress so net/http does not signal that the partial body is complete.
// It logs its own deadline when the parent request is still active.
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

			done := make(chan struct{})
			tw := newTimeoutWriter(ctx, w)

			go func() {
				defer close(done)
				defer finishTimeoutHandler(tw, r)
				next.ServeHTTP(tw, r.WithContext(ctx))
			}()

			select {
			case <-done:
			case <-ctx.Done():
			}
			// A handler can return as soon as its context expires. Use the
			// recorded outcome, independent of which channel woke us.
			tw.mu.Lock()
			defer tw.mu.Unlock()
			if tw.finished && !tw.abort {
				return
			}
			if r.Context().Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				if !tw.abort && tw.responseComplete(r.Method) {
					return
				}
				slog.WarnContext(r.Context(), "request timed out", "method", r.Method, "path", r.URL.Path,
					"request_id", chimw.GetReqID(r.Context()), "response_started", tw.wroteHeader, "timeout", timeout)
			}
			if tw.wroteHeader || tw.abort {
				panic(http.ErrAbortHandler)
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("Request timeout"))
		})
	}
}

// finishTimeoutHandler records completion before the deadline and recovers
// panics in the handler goroutine. Before the deadline a panic produces a
// clean 500 or an abort if the response started. An explicit abort is passed
// through without a panic log.
func finishTimeoutHandler(tw *timeoutWriter, r *http.Request) {
	p := recover()
	err, _ := p.(error)
	abort := errors.Is(err, http.ErrAbortHandler)
	if p != nil && !abort {
		slog.Error("panic serving request", "panic", p, "method", r.Method, "path", r.URL.Path,
			"request_id", chimw.GetReqID(r.Context()), "stack", string(debug.Stack()))
	}
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.ctx.Err() != nil {
		return
	}
	tw.finished = true
	switch {
	case p == nil:
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
// when the response starts. When the request context expires, subsequent
// WriteHeader, Write and Flush calls are dropped.
type timeoutWriter struct {
	http.ResponseWriter
	ctx         context.Context
	header      http.Header
	mu          sync.Mutex
	wroteHeader bool
	status      int
	remaining   int64 // -1 when the body length is unknown
	finished    bool
	abort       bool
}

// newTimeoutWriter returns a timeoutWriter for w, starting from the headers
// set on w so far.
func newTimeoutWriter(ctx context.Context, w http.ResponseWriter) *timeoutWriter {
	return &timeoutWriter{ResponseWriter: w, ctx: ctx, header: w.Header().Clone()}
}

// Header returns the handler's header map.
func (tw *timeoutWriter) Header() http.Header {
	return tw.header
}

// startResponse copies the handler's headers to the underlying writer and
// writes the status code. tw.mu must be held.
func (tw *timeoutWriter) startResponse(code int) {
	tw.wroteHeader = true
	tw.status = code
	tw.remaining = -1
	if lengths := tw.header.Values("Content-Length"); len(lengths) == 1 &&
		tw.header.Get("Transfer-Encoding") == "" && tw.header.Get("Trailer") == "" {
		if length, err := strconv.ParseInt(lengths[0], 10, 64); err == nil && length >= 0 {
			tw.remaining = length
		}
	}
	dst := tw.ResponseWriter.Header()
	clear(dst)
	maps.Copy(dst, tw.header.Clone())
	tw.ResponseWriter.WriteHeader(code)
}

// responseComplete is called under mu. A body with unknown length still
// needs the handler to return before we can safely end its response.
func (tw *timeoutWriter) responseComplete(method string) bool {
	return tw.status >= 200 && (method == http.MethodHead ||
		tw.status == http.StatusNoContent || tw.status == http.StatusNotModified || tw.remaining == 0)
}

func (tw *timeoutWriter) FlushError() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.ctx.Err() != nil {
		return http.ErrHandlerTimeout
	}
	if !tw.wroteHeader {
		tw.startResponse(http.StatusOK)
	}
	return http.NewResponseController(tw.ResponseWriter).Flush()
}

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if !tw.wroteHeader && tw.ctx.Err() == nil {
		tw.startResponse(code)
	}
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.ctx.Err() != nil {
		return 0, http.ErrHandlerTimeout
	}
	if !tw.wroteHeader {
		// If no Content-Type set, default to plain text to prevent XSS
		if tw.header.Get("Content-Type") == "" {
			tw.header.Set("Content-Type", "text/plain; charset=utf-8")
		}
		tw.startResponse(http.StatusOK)
	}
	n, err := tw.ResponseWriter.Write(b)
	if tw.remaining >= 0 {
		if err != nil || n != len(b) || int64(n) > tw.remaining || tw.ctx.Err() != nil {
			tw.remaining = -1
		} else {
			tw.remaining -= int64(n)
		}
	}
	return n, err
}
