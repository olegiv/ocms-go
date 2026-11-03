// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func cimdDocument(id string) registeredClient {
	return registeredClient{ID: id, Name: "Client", AuthMethod: "none", Redirects: []string{"https://client.test/callback"}}
}

// TestCIMDRequiresJSONContentType pins the Content-Type part of audit A-7:
// a metadata URL answering with HTML or plain text is not a metadata document,
// even when its body happens to parse.
func TestCIMDRequiresJSONContentType(t *testing.T) {
	const id = "https://client.test/cimd"
	for _, tc := range []struct {
		name, header string
		suppress     bool // no header at all, so the server sniffs text/plain
		want         string
	}{
		{name: "json", header: "application/json"},
		{name: "json with charset", header: "application/json; charset=utf-8"},
		{name: "structured suffix", header: "application/jrd+json"},
		{name: "sniffed text", suppress: true, want: "invalid_client"},
		{name: "plain text", header: "text/plain; charset=utf-8", want: "invalid_client"},
		{name: "html", header: "text/html", want: "invalid_client"},
		{name: "wrong application type", header: "application/x-www-form-urlencoded", want: "invalid_client"},
		{name: "malformed", header: "application/json; charset", want: "invalid_client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := metadataFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				if !tc.suppress {
					w.Header().Set("Content-Type", tc.header)
				}
				_ = json.NewEncoder(w).Encode(cimdDocument(id))
			})
			_, err := f.s.clients.resolve(t.Context(), id)
			if got := problemCode(err); got != tc.want || (tc.want == "" && err != nil) {
				t.Fatalf("resolve error = %v (code %q), want code %q", err, got, tc.want)
			}
		})
	}
}

// TestCIMDFailuresAreRememberedBriefly pins the negative cache of audit A-7:
// the same failing URL is not fetched again within cimdFailureTTL, the code
// the first answer carried (a transport fault stays retryable) is kept, and
// the URL is fetched again afterwards.
func TestCIMDFailuresAreRememberedBriefly(t *testing.T) {
	const id = "https://client.test/cimd"
	for _, tc := range []struct {
		name   string
		status int
		fault  bool
		want   string
	}{
		{name: "not found", status: http.StatusNotFound, want: "invalid_client"},
		{name: "forbidden", status: http.StatusForbidden, want: "invalid_client"},
		{name: "unavailable", status: http.StatusServiceUnavailable, want: "temporarily_unavailable"},
		{name: "rate limited", status: http.StatusTooManyRequests, want: "temporarily_unavailable"},
		{name: "transport fault", fault: true, want: "temporarily_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			f := metadataFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
			})
			if tc.fault {
				f.s.clients.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls.Add(1)
					return nil, errors.New("connection refused")
				})
			}
			for attempt := range 3 {
				if got := problemCode(second(f.s.clients.resolve(t.Context(), id))); got != tc.want {
					t.Fatalf("attempt %d error = %q, want %q", attempt, got, tc.want)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("a failing metadata URL was fetched %d times within %s", calls.Load(), cimdFailureTTL)
			}
			if _, err := f.s.clients.resolve(t.Context(), "https://client.test/other"); err == nil || calls.Load() != 2 {
				t.Fatalf("one URL's failure was applied to another: calls=%d", calls.Load())
			}
			later := time.Now().Add(cimdFailureTTL + time.Second)
			f.s.now = func() time.Time { return later }
			if got := problemCode(second(f.s.clients.resolve(t.Context(), id))); got != tc.want || calls.Load() != 3 {
				t.Fatalf("after the TTL: error = %q calls=%d, want a fresh fetch", got, calls.Load())
			}
		})
	}
}

func second(_ registeredClient, err error) error { return err }

// TestCIMDStorageFaultIsNotRemembered keeps the negative cache to the remote
// answer: a failure to save a good document is this server's fault, not the
// URL's, so the next request must try again at once.
func TestCIMDStorageFaultIsNotRemembered(t *testing.T) {
	const id = "https://client.test/cimd"
	var calls atomic.Int64
	f := metadataFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cimdDocument(id))
	})
	if _, err := f.db.Exec(`CREATE TRIGGER reject_client BEFORE INSERT ON oauth_clients BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if got := problemCode(second(f.s.clients.resolve(t.Context(), id))); got != "temporarily_unavailable" {
		t.Fatalf("storage fault answered %q", got)
	}
	if _, err := f.db.Exec(`DROP TRIGGER reject_client`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.clients.resolve(t.Context(), id); err != nil {
		t.Fatalf("storage fault was remembered as a failed fetch: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("fetches = %d, want 2", calls.Load())
	}
}

// TestCIMDFailureMemoryIsBounded keeps an attacker naming endless URLs on an
// allowed host from growing the negative cache.
func TestCIMDFailureMemoryIsBounded(t *testing.T) {
	var calls atomic.Int64
	f := metadataFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	})
	remembered := func() int {
		f.s.clients.failuresMu.Lock()
		defer f.s.clients.failuresMu.Unlock()
		return len(f.s.clients.failures)
	}
	for i := range cimdFailureCapacity + 50 {
		if _, err := f.s.clients.resolve(t.Context(), fmt.Sprintf("https://client.test/doc-%d", i)); err == nil {
			t.Fatal("missing document accepted")
		}
	}
	if got := remembered(); got != cimdFailureCapacity {
		t.Fatalf("remembered failures = %d, want the cap %d", got, cimdFailureCapacity)
	}
	// A URL that did not fit is fetched again, rather than refused.
	before := calls.Load()
	if _, err := f.s.clients.resolve(t.Context(), fmt.Sprintf("https://client.test/doc-%d", cimdFailureCapacity+49)); err == nil || calls.Load() != before+1 {
		t.Fatalf("an unremembered failure was not retried: calls %d -> %d", before, calls.Load())
	}
	// Stale entries make room once their time has passed.
	later := time.Now().Add(cimdFailureTTL + time.Second)
	f.s.now = func() time.Time { return later }
	if _, err := f.s.clients.resolve(t.Context(), "https://client.test/after"); err == nil {
		t.Fatal("missing document accepted")
	}
	if got := remembered(); got != 1 {
		t.Fatalf("remembered failures after expiry = %d, want 1", got)
	}
}

// TestCIMDFetchOutlivesTheCallerThatStartedIt pins the context part of audit
// A-7: waiters share one flight, so the first caller giving up must not fail
// the others.
func TestCIMDFetchOutlivesTheCallerThatStartedIt(t *testing.T) {
	const id = "https://client.test/cimd"
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var calls atomic.Int64
	f := metadataFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cimdDocument(id))
	})
	t.Cleanup(unblock)

	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	firstErr := make(chan error, 1)
	go func() {
		_, err := f.s.clients.resolve(firstCtx, id)
		firstErr <- err
	}()
	<-started

	type result struct {
		client registeredClient
		err    error
	}
	secondResult := make(chan result, 1)
	go func() {
		client, err := f.s.clients.resolve(t.Context(), id)
		secondResult <- result{client, err}
	}()
	time.Sleep(100 * time.Millisecond) // let the second caller join the flight

	cancelFirst()
	if got := problemCode(<-firstErr); got != "temporarily_unavailable" {
		t.Fatalf("cancelled caller answered %q", got)
	}
	unblock()
	got := <-secondResult
	if got.err != nil || got.client.ID != id {
		t.Fatalf("the caller that did not cancel failed: %v", got.err)
	}
	if calls.Load() != 1 {
		t.Fatalf("metadata fetched %d times for two callers", calls.Load())
	}
}
