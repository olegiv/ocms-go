package middleware

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// StaticCache adds Cache-Control headers for static files.
func StaticCache(maxAge int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(maxAge))
			next.ServeHTTP(w, r)
		})
	}
}

// etagResponseWriter captures the response to calculate ETag.
type etagResponseWriter struct {
	http.ResponseWriter
	buf        bytes.Buffer
	statusCode int
}

func (e *etagResponseWriter) WriteHeader(statusCode int) {
	e.statusCode = statusCode
}

func (e *etagResponseWriter) Write(b []byte) (int, error) {
	return e.buf.Write(b)
}

// etagCache caches ETags for static files to avoid recalculating.
var etagCache = struct {
	sync.RWMutex
	items map[string]string
}{
	items: make(map[string]string),
}

// ETag adds ETag headers and handles conditional requests for static files.
// This middleware should be used after StaticCache.
func ETag() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only handle GET and HEAD requests
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}

			// Wrap response writer to capture content
			ew := &etagResponseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(ew, r)

			// Only process successful responses
			if ew.statusCode != http.StatusOK {
				w.WriteHeader(ew.statusCode)
				w.Write(ew.buf.Bytes())
				return
			}

			// Calculate ETag from content hash
			hash := md5.Sum(ew.buf.Bytes())
			etag := `"` + hex.EncodeToString(hash[:]) + `"`

			// Check If-None-Match header
			if match := r.Header.Get("If-None-Match"); match != "" {
				// Remove weak ETag prefix if present
				match = strings.TrimPrefix(match, "W/")
				if match == etag {
					w.Header().Set("ETag", etag)
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}

			// Set ETag header and write response
			w.Header().Set("ETag", etag)
			w.WriteHeader(ew.statusCode)
			w.Write(ew.buf.Bytes())
		})
	}
}

// ETagFromFS serves files from an fs.FS with ETag support based on file content.
// This is more efficient than the generic ETag middleware as it doesn't buffer
// the entire response.
func ETagFromFS(fsys fs.FS, prefix string, maxAge int) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	if prefix != "" {
		fileServer = http.StripPrefix(prefix, fileServer)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set cache headers
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(maxAge))

		// Get the file path
		path := strings.TrimPrefix(r.URL.Path, prefix)
		path = strings.TrimPrefix(path, "/")

		// Try to get cached ETag
		etagCache.RLock()
		cachedETag, found := etagCache.items[path]
		etagCache.RUnlock()

		if found {
			// Check If-None-Match
			if match := r.Header.Get("If-None-Match"); match != "" {
				match = strings.TrimPrefix(match, "W/")
				if match == cachedETag {
					w.Header().Set("ETag", cachedETag)
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
			w.Header().Set("ETag", cachedETag)
		}

		fileServer.ServeHTTP(w, r)
	})
}

// GenerateETag generates an ETag for given content using MD5 hash.
func GenerateETag(content []byte) string {
	hash := md5.Sum(content)
	return `"` + hex.EncodeToString(hash[:]) + `"`
}

// GenerateETagFromReader generates an ETag from an io.Reader.
func GenerateETagFromReader(r io.Reader) (string, error) {
	hash := md5.New()
	if _, err := io.Copy(hash, r); err != nil {
		return "", err
	}
	return `"` + hex.EncodeToString(hash.Sum(nil)) + `"`, nil
}
