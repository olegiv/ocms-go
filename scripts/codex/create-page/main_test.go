// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/middleware"
)

const fixtureKey = "synthetic-fixture-page-api-key="

func keyFileForTest(t *testing.T, contents string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private key")
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCreatePageOriginPolicy(t *testing.T) {
	for _, address := range []string{
		"http://pages.example.test",
		"http://localhost.pages.example.test",
		"http://192.168.1.2",
		"http://[::ffff:192.168.1.2]",
		"http://127.1",
		"http://localhost@pages.example.test",
		"https://name:secret@pages.example.test",
		"https://pages.example.test/api",
		"https://pages.example.test/?",
		"https://pages.example.test/#",
		"https://:443",
		"https://pages.example.test:000",
		"https://pages.example.test:65536",
		"file:///tmp/pages",
	} {
		t.Run(address, func(t *testing.T) {
			if _, err := pageEndpoint(address); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid origin accepted or echoed: %v", err)
			}
		})
	}
	for _, address := range []string{
		"https://pages.example.test:8443/",
		"http://localhost:8080/",
		"http://127.0.0.2",
		"http://[::1]:8080",
	} {
		t.Run(address, func(t *testing.T) {
			endpoint, err := pageEndpoint(address)
			if err != nil || endpoint.Path != "/api/v2/pages" || endpoint.User != nil {
				t.Fatalf("valid site did not resolve to the create route: %v, %v", endpoint, err)
			}
		})
	}
}

func TestCreatePageRequiresPrivateKeyFile(t *testing.T) {
	private := keyFileForTest(t, fixtureKey+"\n", 0o600)
	if key, err := readKey(private); err != nil || key != fixtureKey {
		t.Fatalf("private key file refused: %v", err)
	}
	link := filepath.Join(t.TempDir(), "linked key")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	public := keyFileForTest(t, fixtureKey, 0o600)
	if err := os.Chmod(public, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, public, keyFileForTest(t, fixtureKey+"\r\nX-Test: injected", 0o600)} {
		if _, err := readKey(path); err == nil || strings.Contains(err.Error(), fixtureKey) {
			t.Fatalf("unsafe key file accepted or echoed: %v", err)
		}
	}
}

func TestCreatePageBatchStopsAndKeepsResults(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/pages" ||
			r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("Authorization") != "Bearer "+fixtureKey {
			t.Error("create request did not use the expected route and headers")
		}
		var page struct {
			Slug string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&page); err != nil {
			t.Error(err)
		}
		if n == 1 && page.Slug == "first-page" {
			w.WriteHeader(http.StatusCreated)
			response := `{"data":{"id":1,"title":"` + fixtureKey + `","slug":"first-page","status":"draft","revision":1}}`
			if _, err := io.WriteString(w, response); err != nil {
				t.Error(err)
			}
			return
		}
		if n != 2 || page.Slug != "conflict-page" {
			t.Error("batch did not stop at the expected failure")
		}
		middleware.WriteAPIError(w, http.StatusConflict, "conflict", "Slug already exists: "+fixtureKey,
			map[string]string{"slug": "Choose another slug: " + fixtureKey, "input." + fixtureKey: "Reflected field"})
	}))
	defer server.Close()
	origin := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	args := []string{"--site-url", origin, "--key-file", keyFileForTest(t, fixtureKey, 0o600)}
	var out bytes.Buffer
	if err := run(t.Context(), args, strings.NewReader(`[{"slug":"first-page"},null]`), &out); err == nil || count.Load() != 0 {
		t.Fatal("invalid batch sent a request before validation")
	}
	prefix := `{"body":"`
	suffix := `"}`
	nearLimit := prefix + strings.Repeat("a", (1<<20)-len(prefix)-len(suffix)-1) + suffix
	if _, err := readPages(strings.NewReader(nearLimit)); err != nil {
		t.Fatalf("page below the size limit refused: %v", err)
	}
	exactLimit := prefix + strings.Repeat("a", (1<<20)-len(prefix)-len(suffix)) + suffix
	for _, oversized := range []string{exactLimit, `[{"slug":"first-page"},` + exactLimit + `]`} {
		err := run(t.Context(), args, strings.NewReader(oversized), &out)
		if err == nil || !strings.Contains(err.Error(), "smaller than 1 MiB") || count.Load() != 0 {
			t.Fatal("oversized input was not refused before sending the batch")
		}
	}
	input := `[{"slug":"first-page"},{"slug":"conflict-page"},{"slug":"never-sent"}]`
	err := run(t.Context(), args, strings.NewReader(input), &out)
	const want = "page 2 of 3: page API returned HTTP 409 (conflict) Slug already exists: [redacted] [input.[redacted]=Reflected field] [slug=Choose another slug: [redacted]]"
	if err == nil || err.Error() != want || count.Load() != 2 {
		t.Fatalf("incorrect failure handling: %v, %d requests", err, count.Load())
	}
	if got := out.String(); got != "{\"id\":1,\"title\":\"[redacted]\",\"slug\":\"first-page\",\"status\":\"draft\",\"revision\":1}\n" {
		t.Fatal("the batch lost the successful result or exposed the key")
	}
}

func TestCreatePageReportsValidationFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refusal := v2.ToHuma(r.Context(), v2.NewValidationError(map[string]string{
			"body.slug": "Use lowercase letters", "body.title": "Title is required",
		}, "Validation failed\r\n\x1b[31m"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		if err := json.NewEncoder(w).Encode(refusal); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	args := []string{"--site-url", server.URL, "--key-file", keyFileForTest(t, fixtureKey, 0o600)}
	var out bytes.Buffer
	err := run(t.Context(), args, strings.NewReader(`{"slug":"Invalid Slug"}`), &out)
	const want = `page 1 of 1: page API returned HTTP 422 (validation_error) Validation failed\r\n\x1b[31m [body.slug=Use lowercase letters] [body.title=Title is required]`
	if err == nil || err.Error() != want || out.Len() != 0 {
		t.Fatalf("validation diagnostic = %q, stdout %q", err, out.String())
	}
}

func TestCreatePageBoundsRefusalOutput(t *testing.T) {
	const maxDiagnosticBytes = 2 << 10
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		middleware.WriteAPIError(w, http.StatusUnprocessableEntity, "validation_error_"+fixtureKey,
			strings.Repeat("я", maxDiagnosticBytes), nil)
	}))
	defer server.Close()
	args := []string{"--site-url", server.URL, "--key-file", keyFileForTest(t, fixtureKey, 0o600)}
	var out bytes.Buffer
	err := run(t.Context(), args, strings.NewReader(`{"slug":"Invalid Slug"}`), &out)
	if err == nil {
		t.Fatal("long refusal was reported as a success")
	}
	const prefix = "page 1 of 1: page API returned HTTP 422 "
	message := err.Error()
	if !strings.HasPrefix(message, prefix+"(validation_error_[redacted]) ") || !strings.HasSuffix(message, "…") ||
		len(message) > len(prefix)+maxDiagnosticBytes || !utf8.ValidString(message) || out.Len() != 0 {
		t.Fatalf("unbounded or invalid refusal diagnostic (%d bytes)", len(message))
	}
}

func TestCreatePageFallsBackForUnusableRefusal(t *testing.T) {
	const valid = `{"error":{"code":"conflict","message":"do not relay ` + fixtureKey + `"}}`
	for _, tt := range []struct {
		name, body string
		truncated  bool
	}{
		{name: "plain text", body: fixtureKey},
		{name: "malformed JSON", body: valid[:len(valid)-1]},
		{name: "wrong envelope", body: `{"error":{"code":"conflict","message":"` + fixtureKey + `","details":[]}}`},
		{name: "missing code", body: `{"error":{"message":"` + fixtureKey + `"}}`},
		{name: "oversized", body: valid + strings.Repeat(" ", 32<<10)},
		{name: "incomplete body", body: valid, truncated: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.truncated {
					w.Header().Set("Content-Length", strconv.Itoa(len(tt.body)+1))
				}
				w.WriteHeader(http.StatusConflict)
				if _, err := io.WriteString(w, tt.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			args := []string{"--site-url", server.URL, "--key-file", keyFileForTest(t, fixtureKey, 0o600)}
			var out bytes.Buffer
			err := run(t.Context(), args, strings.NewReader(`{"slug":"taken"}`), &out)
			if err == nil || err.Error() != "page 1 of 1: page API returned HTTP 409" || out.Len() != 0 {
				t.Fatalf("unusable refusal diagnostic = %q, stdout %q", err, out.String())
			}
		})
	}
}

func TestCreatePageDoesNotClaimAnUncertainResult(t *testing.T) {
	const diagnostic = `{"error":{"code":"failure","message":"Server diagnostics ` + fixtureKey + `"}}`
	for _, tt := range []struct {
		status int
		body   string
	}{
		{http.StatusCreated, `{"data":{"id":1}}`},
		{http.StatusBadGateway, diagnostic},
		{http.StatusRequestTimeout, diagnostic},
	} {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				if _, err := io.WriteString(w, tt.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			args := []string{"--site-url", server.URL, "--key-file", keyFileForTest(t, fixtureKey, 0o600)}
			var out bytes.Buffer
			err := run(t.Context(), args, strings.NewReader(`{"slug":"unknown"}`), &out)
			if err == nil || !strings.Contains(err.Error(), "before retrying") ||
				strings.Contains(err.Error(), fixtureKey) || strings.Contains(err.Error(), "Server diagnostics") || out.Len() != 0 {
				t.Fatalf("uncertain result was reported as a success or exposed the key: %v", err)
			}
		})
	}
}

func TestCreatePageDebugLoggingDoesNotExposeKey(t *testing.T) {
	const originEnv = "OCMS_CREATE_PAGE_TEST_TLS_ORIGIN"
	const certEnv = "OCMS_CREATE_PAGE_TEST_TLS_CERT"
	if origin := os.Getenv(originEnv); origin != "" {
		cert, err := os.ReadFile(os.Getenv(certEnv))
		if err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(cert) {
			t.Fatal("invalid TLS fixture certificate")
		}
		endpoint, err := pageEndpoint(origin)
		if err != nil {
			t.Fatal(err)
		}
		client := pageClient(endpoint)
		defer client.CloseIdleConnections()
		client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		if _, err = createPage(t.Context(), client, endpoint, fixtureKey, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		return
	}
	var version atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version.Store(int32(r.ProtoMajor))
		if r.Header.Get("Authorization") != "Bearer "+fixtureKey {
			t.Error("TLS control did not authenticate")
		}
		w.WriteHeader(http.StatusCreated)
		if _, err := io.WriteString(w, `{"data":{"id":1,"slug":"test","status":"draft","revision":1}}`); err != nil {
			t.Error(err)
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	certFile := filepath.Join(t.TempDir(), "root.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(certFile, cert, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCreatePageDebugLoggingDoesNotExposeKey$")
	cmd.Env = append(os.Environ(), "GODEBUG=http2debug=2", originEnv+"="+server.URL, certEnv+"="+certFile)
	out, err := cmd.CombinedOutput()
	if bytes.Contains(out, []byte(fixtureKey)) {
		t.Fatal("HTTP debug output exposed the API key")
	}
	if err != nil || version.Load() != 1 {
		t.Fatalf("trusted HTTPS request did not succeed over HTTP/1: %v", err)
	}
}

func TestCreatePageRequiresVerifiedTLSAndStopsAtRedirect(t *testing.T) {
	var redirected, requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+fixtureKey {
			t.Error("HTTPS request did not authenticate")
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	endpoint, err := pageEndpoint(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := pageClient(endpoint)
	defer client.CloseIdleConnections()
	if _, err = createPage(t.Context(), client, endpoint, fixtureKey, json.RawMessage(`{}`)); err == nil || requests.Load() != 0 {
		t.Fatal("untrusted TLS reached the page API")
	}
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("page credentials could be sent through an ambient proxy")
	}
	transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport.TLSClientConfig.RootCAs = x509.NewCertPool()
	transport.TLSClientConfig.RootCAs.AddCert(server.Certificate())
	if _, err = createPage(t.Context(), client, endpoint, fixtureKey, json.RawMessage(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "HTTP 307") || requests.Load() != 1 || redirected.Load() != 0 {
		t.Fatalf("redirect was followed or the trusted HTTPS control failed: %v", err)
	}
}
