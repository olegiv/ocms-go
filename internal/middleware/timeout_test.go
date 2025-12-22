package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestTimeoutWriter creates a timeoutWriter for testing.
func newTestTimeoutWriter() (*timeoutWriter, *httptest.ResponseRecorder) {
	rr := httptest.NewRecorder()
	return &timeoutWriter{ResponseWriter: rr}, rr
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
