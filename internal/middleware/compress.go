package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// gzipResponseWriter wraps http.ResponseWriter to provide gzip compression.
type gzipResponseWriter struct {
	http.ResponseWriter
	gzipWriter *gzip.Writer
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	return g.gzipWriter.Write(b)
}

// gzipWriterPool pools gzip.Writer instances to reduce allocations.
var gzipWriterPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(io.Discard)
	},
}

// Compress is a middleware that gzip-compresses response bodies for clients
// that support it. It skips compression for already compressed content types
// like images, videos, and compressed archives.
func Compress(level int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip if client doesn't accept gzip
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}

			// Get gzip writer from pool
			gz := gzipWriterPool.Get().(*gzip.Writer)
			gz.Reset(w)
			defer func() {
				_ = gz.Close()
				gzipWriterPool.Put(gz)
			}()

			// Set required headers
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Vary", "Accept-Encoding")
			// Remove Content-Length as it will change after compression
			w.Header().Del("Content-Length")

			// Wrap response writer
			gzw := &gzipResponseWriter{
				ResponseWriter: w,
				gzipWriter:     gz,
			}

			next.ServeHTTP(gzw, r)
		})
	}
}

// compressibleContentTypes lists content types that should be compressed.
var compressibleContentTypes = []string{
	"text/html",
	"text/css",
	"text/plain",
	"text/javascript",
	"application/javascript",
	"application/json",
	"application/xml",
	"text/xml",
	"image/svg+xml",
	"application/rss+xml",
	"application/atom+xml",
}

// CompressSelective is a middleware that only compresses responses with
// compressible content types. This is more efficient for mixed content.
func CompressSelective(level int, minSize int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip if client doesn't accept gzip
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}

			// Use a custom response writer that buffers and decides whether to compress
			sw := &selectiveWriter{
				ResponseWriter: w,
				request:        r,
				level:          level,
				minSize:        minSize,
			}

			next.ServeHTTP(sw, r)

			// Flush any remaining buffered content
			sw.Flush()
		})
	}
}

// selectiveWriter buffers responses and only compresses if appropriate.
type selectiveWriter struct {
	http.ResponseWriter
	request    *http.Request
	level      int
	minSize    int
	buffer     []byte
	statusCode int
}

func (sw *selectiveWriter) WriteHeader(statusCode int) {
	sw.statusCode = statusCode
}

func (sw *selectiveWriter) Write(b []byte) (int, error) {
	sw.buffer = append(sw.buffer, b...)
	return len(b), nil
}

func (sw *selectiveWriter) Flush() {
	if len(sw.buffer) == 0 {
		return
	}

	contentType := sw.Header().Get("Content-Type")
	shouldCompress := len(sw.buffer) >= sw.minSize && isCompressible(contentType)

	if shouldCompress {
		sw.Header().Set("Content-Encoding", "gzip")
		sw.Header().Set("Vary", "Accept-Encoding")
		sw.Header().Del("Content-Length")
	}

	if sw.statusCode != 0 {
		sw.ResponseWriter.WriteHeader(sw.statusCode)
	}

	if shouldCompress {
		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(sw.ResponseWriter)
		_, _ = gz.Write(sw.buffer)
		_ = gz.Close()
		gzipWriterPool.Put(gz)
	} else {
		_, _ = sw.ResponseWriter.Write(sw.buffer)
	}
}

// isCompressible checks if the content type should be compressed.
func isCompressible(contentType string) bool {
	if contentType == "" {
		return false
	}

	// Extract the media type without parameters (e.g., charset)
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = strings.TrimSpace(contentType[:idx])
	}

	for _, ct := range compressibleContentTypes {
		if strings.EqualFold(contentType, ct) {
			return true
		}
	}

	// Also compress text/* types
	if strings.HasPrefix(strings.ToLower(contentType), "text/") {
		return true
	}

	return false
}
