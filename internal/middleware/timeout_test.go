// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// newTestTimeoutWriter creates a timeoutWriter for testing.
func newTestTimeoutWriter() (*timeoutWriter, *httptest.ResponseRecorder) {
	rr := httptest.NewRecorder()
	return newTimeoutWriter(rr), rr
}

func TestTimeoutMiddlewareNormalRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	})

	middleware := Timeout(5 * time.Second)
	wrapped := middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	if body := rr.Body.String(); body != "success" {
		t.Errorf("Body = %q, want %q", body, "success")
	}
}

func TestTimeoutMiddlewareSlowRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow handler that respects context
		select {
		case <-time.After(5 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			return
		}
	})

	middleware := Timeout(50 * time.Millisecond)
	wrapped := middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	if body := rr.Body.String(); body != "Request timeout" {
		t.Errorf("Body = %q, want %q", body, "Request timeout")
	}
}

func TestTimeoutMiddlewareWithHeaders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "test-value")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	})

	middleware := Timeout(5 * time.Second)
	wrapped := middleware(handler)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusCreated)
	}

	if h := rr.Header().Get("X-Custom-Header"); h != "test-value" {
		t.Errorf("X-Custom-Header = %q, want %q", h, "test-value")
	}
}

func TestTimeoutWriterWriteHeader(t *testing.T) {
	tw, rr := newTestTimeoutWriter()

	// First WriteHeader should work
	tw.WriteHeader(http.StatusOK)
	if !tw.wroteHeader {
		t.Error("wroteHeader should be true after WriteHeader")
	}

	// Second WriteHeader should be ignored
	tw.WriteHeader(http.StatusNotFound)
	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d (second WriteHeader should be ignored)", rr.Code, http.StatusOK)
	}
}

func TestTimeoutWriterFlush(t *testing.T) {
	for _, status := range []int{0, http.StatusCreated} {
		tw, rr := newTestTimeoutWriter()
		want := status
		if status != 0 {
			tw.WriteHeader(status)
		} else {
			want = http.StatusOK
		}
		if err := http.NewResponseController(tw).Flush(); err != nil {
			t.Fatal(err)
		}
		if !tw.wroteHeader || !rr.Flushed || rr.Code != want || rr.Body.Len() != 0 {
			t.Errorf("flush tracking=%t flushed=%t status=%d bytes=%d", tw.wroteHeader, rr.Flushed, rr.Code, rr.Body.Len())
		}
	}
	t.Run("unsupported writer", func(t *testing.T) {
		rr := httptest.NewRecorder()
		tw := newTimeoutWriter(struct{ http.ResponseWriter }{rr})
		if err := http.NewResponseController(tw).Flush(); !errors.Is(err, http.ErrNotSupported) {
			t.Fatalf("Flush error=%v, want unsupported", err)
		}
	})
}

func TestTimeoutWriterWrite(t *testing.T) {
	tw, rr := newTestTimeoutWriter()

	// Write without WriteHeader should set 200
	n, err := tw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 5 {
		t.Errorf("Write() = %d, want 5", n)
	}
	if !tw.wroteHeader {
		t.Error("wroteHeader should be true after Write")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestTimeoutWriterWriteAfterWriteHeader(t *testing.T) {
	tw, rr := newTestTimeoutWriter()

	tw.WriteHeader(http.StatusCreated)
	_, _ = tw.Write([]byte("created"))

	if rr.Code != http.StatusCreated {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusCreated)
	}
	if body := rr.Body.String(); body != "created" {
		t.Errorf("Body = %q, want %q", body, "created")
	}
}

// syncBuffer is a log destination safe for concurrent writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog sends the default slog logger, and the standard log package it
// takes over, to a buffer until the test ends.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	previous, writer, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
	return buf
}

// serveRecovering serves r with handler and returns what it panicked with.
func serveRecovering(handler http.Handler, w http.ResponseWriter, r *http.Request) (recovered any) {
	defer func() { recovered = recover() }()
	handler.ServeHTTP(w, r)
	return nil
}

// lateCallWriter records the calls that reach it once the handler serving
// it has returned, when net/http owns the response.
type lateCallWriter struct {
	*httptest.ResponseRecorder
	mu       sync.Mutex
	returned bool
	late     []string
}

func (w *lateCallWriter) record(call string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.returned {
		w.late = append(w.late, call)
	}
}

// markReturned starts recording.
func (w *lateCallWriter) markReturned() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.returned = true
}

// lateCalls returns the calls recorded.
func (w *lateCallWriter) lateCalls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.late
}

func (w *lateCallWriter) WriteHeader(code int) {
	w.record("WriteHeader")
	w.ResponseRecorder.WriteHeader(code)
}

func (w *lateCallWriter) Write(b []byte) (int, error) {
	w.record("Write")
	return w.ResponseRecorder.Write(b)
}

func (w *lateCallWriter) Flush() {
	w.record("Flush")
	w.ResponseRecorder.Flush()
}

// TestTimeoutRecoversHandlerPanics checks that a handler panic behind
// Timeout is recovered, logged with its stack and request ID, and answered
// with 500. The handler runs in Timeout's own goroutine, where chi's
// Recoverer in front of Timeout cannot catch it, so a panic in any handler
// used to end the process (review of the 2026-10-17 audit fixes). The 500
// keeps the headers set in front of Timeout and drops those the handler set
// before panicking: a Cache-Control would let caches keep the error, and a
// Content-Disposition would download it.
//
// Bug state: drop the deferred recoverHandlerPanic in Timeout and the test
// binary crashes; call tw.startResponse for the 500 and this fails.
func TestTimeoutRecoversHandlerPanics(t *testing.T) {
	logs := captureLog(t)
	handler := chimw.RequestID(chimw.Recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		Timeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("Content-Disposition", "attachment; filename=export.csv")
			panic("boom")
		})).ServeHTTP(w, r)
	})))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/crash", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d after a handler panic, want 500", rr.Code)
	}
	if got := rr.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q on the 500, want the DENY set in front of Timeout", got)
	}
	for _, name := range []string{"Cache-Control", "Content-Disposition"} {
		if got := rr.Header().Get(name); got != "" {
			t.Errorf("the 500 kept the %s the handler set: %q", name, got)
		}
	}
	for _, want := range []string{`msg="panic serving request"`, "panic=boom", "path=/crash", "request_id=", "stack="} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the panic was logged without %s:\n%s", want, logs.String())
		}
	}
	if strings.Contains(logs.String(), "request_id=\"\"") || strings.Contains(logs.String(), "request_id= ") {
		t.Errorf("the panic was logged without its request ID:\n%s", logs.String())
	}
}

// TestTimeoutAbortsPartialResponsesOnPanic fails when a handler that panics
// after starting its response has that response end normally: clients and
// caches would take the partial body for the whole page. Timeout logs the
// panic and passes it on as http.ErrAbortHandler, so net/http drops the
// connection, as it does for a panic without Timeout.
//
// Bug state: drop tw.wroteHeader from the abort case in recoverHandlerPanic
// and this fails.
func TestTimeoutAbortsPartialResponsesOnPanic(t *testing.T) {
	logs := captureLog(t)
	handler := Timeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	}))
	rr := httptest.NewRecorder()
	recovered := serveRecovering(handler, rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if err, _ := recovered.(error); !errors.Is(err, http.ErrAbortHandler) {
		t.Errorf("Timeout passed on %v after a panic in a started response, want http.ErrAbortHandler", recovered)
	}
	if body := rr.Body.String(); body != "partial" {
		t.Errorf("body = %q, want only what the handler wrote", body)
	}
	if !strings.Contains(logs.String(), "panic=boom") {
		t.Errorf("the panic was not logged:\n%s", logs.String())
	}
}

// TestTimeoutKeepsHandlerHeadersApart fails when the handler sets its headers
// in the map Timeout answers with. A handler still setting headers when the
// request times out then writes that map while Timeout does, and concurrent
// map writes end the process with a fatal error no recover can catch (review
// of the 2026-10-17 audit fixes).
//
// Bug state: return tw.ResponseWriter.Header() from timeoutWriter.Header and
// "after the answer" fails; under -race "while Timeout answers" reports the
// race.
func TestTimeoutKeepsHandlerHeadersApart(t *testing.T) {
	handlerHeaders := []string{"X-Late", "X-Content-Type-Options"}
	// serve runs a handler that sets headers once wait returns; served runs
	// after Timeout has returned.
	serve := func(t *testing.T, wait func(*http.Request), served func()) *httptest.ResponseRecorder {
		t.Helper()
		finished := make(chan struct{})
		handler := Timeout(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(finished)
			wait(r)
			http.Error(w, "late", http.StatusGatewayTimeout)
			w.Header().Set("X-Late", "1")
		}))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
		served()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("the handler did not finish")
		}
		return rr
	}
	checkTimeoutAnswer := func(t *testing.T, rr *httptest.ResponseRecorder) {
		t.Helper()
		for _, name := range handlerHeaders {
			if got := rr.Header().Get(name); got != "" {
				t.Errorf("the 503 timeout answer carries the handler's %s: %q", name, got)
			}
		}
	}

	t.Run("after the answer", func(t *testing.T) {
		release := make(chan struct{})
		rr := serve(t, func(r *http.Request) {
			<-r.Context().Done()
			<-release
		}, func() { close(release) })
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want the 503 timeout answer", rr.Code)
		}
		checkTimeoutAnswer(t, rr)
	})

	t.Run("while Timeout answers", func(t *testing.T) {
		for range 20 {
			rr := serve(t, func(r *http.Request) { <-r.Context().Done() }, func() {})
			switch rr.Code {
			case http.StatusGatewayTimeout: // the handler answered first
			case http.StatusServiceUnavailable:
				checkTimeoutAnswer(t, rr)
			default:
				t.Errorf("status = %d, want the handler's 504 or the 503 timeout answer", rr.Code)
			}
		}
	})
}

// TestTimeoutRecoversLatePanics checks that a handler which panics after
// Timeout has answered 503 is recovered and logged, without writing to the
// response Timeout already sent.
func TestTimeoutRecoversLatePanics(t *testing.T) {
	logs := captureLog(t)
	release, panicked := make(chan struct{}), make(chan struct{})
	handler := Timeout(10 * time.Millisecond)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		<-release
		defer close(panicked)
		panic("late boom")
	}))
	w := &lateCallWriter{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	w.markReturned()
	close(release)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the 503 timeout answer", w.Code)
	}
	select {
	case <-panicked:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not reach its panic")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "panic=\"late boom\"") {
		if time.Now().After(deadline) {
			t.Fatalf("the late panic was not logged:\n%s", logs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if late := w.lateCalls(); len(late) > 0 {
		t.Errorf("the late panic reached the sent response: %v", late)
	}
}

// TestTimeoutPassesOnAbort checks that a handler panicking with
// http.ErrAbortHandler, which net/http treats as a deliberate abort, is not
// logged as a crash and is passed on to the caller so the connection is
// dropped as intended.
func TestTimeoutPassesOnAbort(t *testing.T) {
	logs := captureLog(t)
	handler := Timeout(time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	recovered := serveRecovering(handler, httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if err, _ := recovered.(error); !errors.Is(err, http.ErrAbortHandler) {
		t.Errorf("Timeout passed on %v, want http.ErrAbortHandler", recovered)
	}
	if strings.Contains(logs.String(), "panic serving request") {
		t.Errorf("a deliberate abort was logged as a panic:\n%s", logs.String())
	}
}

// TestTimeoutDropsWritesAfterTheTimeout checks that the WriteHeader, Write
// and Flush calls of a handler still running after Timeout has answered 503
// are dropped with http.ErrHandlerTimeout instead of reaching a response
// writer net/http owns once Timeout has returned.
func TestTimeoutDropsWritesAfterTheTimeout(t *testing.T) {
	release := make(chan struct{})
	results := make(chan []error, 1)
	handler := Timeout(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		<-release
		w.WriteHeader(http.StatusTeapot)
		_, writeErr := w.Write([]byte("too late"))
		results <- []error{writeErr, http.NewResponseController(w).Flush()}
	}))
	w := &lateCallWriter{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	w.markReturned()
	close(release)
	select {
	case errs := <-results:
		for _, err := range errs {
			if !errors.Is(err, http.ErrHandlerTimeout) {
				t.Errorf("a write or flush after the timeout returned %v, want http.ErrHandlerTimeout", err)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never wrote")
	}
	if late := w.lateCalls(); len(late) > 0 {
		t.Errorf("after Timeout returned, the handler's %v reached the response writer", late)
	}
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "too late") {
		t.Errorf("response = %d %q, want only the 503 timeout answer", w.Code, w.Body.String())
	}
}
