// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Command create-page submits page JSON through the oCMS REST write boundary.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/olegiv/ocms-go/internal/contentpath"
)

const (
	maxKeyBytes       = 4096
	maxPageBytes      = 1 << 20 // Huma rejects JSON bodies at or above 1 MiB.
	maxDocumentBytes  = 8 << 20
	maxRefusalBody    = 16 << 10
	maxRefusalMessage = 2 << 10
	requestTimeout    = 30 * time.Second
)

var bearerToken = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)

type pageSummary struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Slug     string `json:"slug"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	flags := flag.NewFlagSet("create-page", flag.ContinueOnError)
	// Avoid echoing arbitrary arguments, including accidentally pasted keys.
	flags.SetOutput(io.Discard)
	site := flags.String("site-url", "", "confirmed oCMS site origin")
	keyFile := flags.String("key-file", "", "owner-only file containing the API key")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *site == "" || *keyFile == "" {
		return errors.New("usage: create-page --site-url ORIGIN --key-file PATH < pages.json")
	}
	endpoint, err := pageEndpoint(*site)
	if err != nil {
		return err
	}
	pages, err := readPages(input)
	if err != nil {
		return err
	}
	key, err := readKey(*keyFile)
	if err != nil {
		return err
	}
	client := pageClient(endpoint)
	defer client.CloseIdleConnections()
	for i, page := range pages {
		summary, createErr := createPage(ctx, client, endpoint, key, page)
		if createErr != nil {
			return fmt.Errorf("page %d of %d: %w", i+1, len(pages), createErr)
		}
		if err = writePageSummary(output, summary); err != nil {
			return fmt.Errorf("page %d was created but its result could not be written: %w", i+1, err)
		}
	}
	return nil
}

func writePageSummary(output io.Writer, summary pageSummary) error {
	data, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	line := make([]byte, 0, len(data))
	// JSON permits C1 and bidi controls in strings. Escape them on the wire
	// so terminals cannot interpret them and JSON readers recover the value.
	for _, r := range string(data) {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			line = fmt.Appendf(line, `\u%04x`, r)
		} else {
			line = utf8.AppendRune(line, r)
		}
	}
	_, err = output.Write(append(line, '\n'))
	return err
}

func pageEndpoint(raw string) (*url.URL, error) {
	origin, err := contentpath.SiteURL(raw)
	if err != nil || strings.ContainsAny(raw, "?#") {
		return nil, errors.New("site URL must be an HTTP(S) origin without credentials, a path, query or fragment")
	}
	if port := origin.Port(); strings.HasSuffix(origin.Host, ":") || (port != "" && strings.Trim(port, "0") == "") {
		return nil, errors.New("site URL port must be in the range 1-65535")
	}
	host := origin.Hostname()
	ip := net.ParseIP(host)
	if origin.Scheme == "http" && !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("site URL must use HTTPS outside loopback")
	}
	return &url.URL{Scheme: origin.Scheme, Host: origin.Host, Path: "/api/v2/pages"}, nil
}

func pageClient(endpoint *url.URL) *http.Client {
	dialer := &net.Dialer{Timeout: requestTimeout}
	// Ignore ambient proxies. Plain HTTP must stay on a direct loopback
	// connection even when the system resolves localhost incorrectly.
	// Go's HTTP/2 debug logging can print the Authorization header.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	transport := &http.Transport{DialContext: dialer.DialContext, Protocols: protocols}
	if endpoint.Scheme == "http" && strings.EqualFold(endpoint.Hostname(), "localhost") {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, candidate := range addrs {
				if candidate.IP.IsLoopback() {
					conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
					if dialErr == nil {
						return conn, nil
					}
				}
			}
			return nil, errors.New("could not connect to localhost on a loopback address")
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func readKey(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("key file must be a regular owner-only file (for example, mode 0600)")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("could not open the API key file")
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) || opened.Mode().Perm()&0o077 != 0 {
		if closeErr := file.Close(); closeErr != nil {
			return "", errors.New("could not close the API key file")
		}
		return "", errors.New("key file changed while opening it")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxKeyBytes+1))
	if closeErr := file.Close(); closeErr != nil || readErr != nil {
		return "", errors.New("could not read the API key file")
	}
	key := strings.TrimSpace(string(data))
	if len(data) > maxKeyBytes || !bearerToken.MatchString(key) {
		return "", errors.New("key file must contain one API bearer token")
	}
	return key, nil
}

func readPages(input io.Reader) ([]json.RawMessage, error) {
	data, err := io.ReadAll(io.LimitReader(input, maxDocumentBytes+1))
	if err != nil || len(data) > maxDocumentBytes {
		return nil, errors.New("could not read page JSON within the 8 MiB limit")
	}
	data = bytes.TrimSpace(data)
	if !json.Valid(data) {
		return nil, errors.New("input must be valid JSON")
	}
	var pages []json.RawMessage
	if data[0] == '{' {
		pages = []json.RawMessage{data}
	} else if err = json.Unmarshal(data, &pages); err != nil || len(pages) == 0 {
		return nil, errors.New("input must be a page object or a nonempty array of page objects")
	}
	for i, page := range pages {
		if bytes.TrimSpace(page)[0] != '{' {
			return nil, fmt.Errorf("page %d must be a JSON object", i+1)
		}
		if len(page) >= maxPageBytes {
			return nil, fmt.Errorf("page %d JSON must be smaller than 1 MiB", i+1)
		}
	}
	return pages, nil
}

func createPage(ctx context.Context, client *http.Client, endpoint *url.URL, key string, page json.RawMessage) (pageSummary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(page))
	if err != nil {
		return pageSummary{}, errors.New("could not prepare the page request")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return pageSummary{}, errors.New("page request failed; check connectivity and TLS, then verify whether it was saved before retrying")
	}
	limit := int64(maxDocumentBytes)
	if resp.StatusCode != http.StatusCreated {
		limit = maxRefusalBody
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	closeErr := resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		if readErr != nil || closeErr != nil || int64(len(body)) > limit {
			body = nil
		}
		return pageSummary{}, pageRefusal(resp, body, key)
	}
	var response struct {
		Data pageSummary `json:"data"`
	}
	if readErr != nil || closeErr != nil || len(body) > maxDocumentBytes || json.Unmarshal(body, &response) != nil {
		return pageSummary{}, errors.New("page API returned HTTP 201 without a readable result; verify the page before retrying")
	}
	summary := response.Data
	if summary.ID <= 0 || summary.Revision <= 0 || summary.Slug == "" || summary.Status == "" {
		return pageSummary{}, errors.New("page API returned HTTP 201 without a complete revision result; verify the page before retrying")
	}
	summary.Title = strings.ReplaceAll(summary.Title, key, "[redacted]")
	summary.Slug = strings.ReplaceAll(summary.Slug, key, "[redacted]")
	summary.Status = strings.ReplaceAll(summary.Status, key, "[redacted]")
	return summary, nil
}

func pageRefusal(resp *http.Response, body []byte, key string) error {
	status := resp.StatusCode
	message := fmt.Sprintf("page API returned HTTP %d", status)
	switch {
	case status >= 500 || status == http.StatusRequestTimeout:
		message += "; verify whether it was saved before retrying"
	case status >= 400:
		reason := pageRefusalReason(body, key)
		if reason == "" {
			reason = "(refusal body unusable: unreadable, over 16 KiB, or not a JSON error envelope)"
		}
		message += " " + reason
	case status >= 300 && status < 400:
		if target, err := resp.Location(); err == nil && (target.Scheme == "https" || target.Scheme == "http") && target.Hostname() != "" && len(target.Host) <= 255 {
			message += "; redirects to " + safeTerminalText(target.Scheme+"://"+target.Host, key)
		}
	}
	if values := resp.Header.Values("Retry-After"); len(values) == 1 && !strings.Contains(values[0], key) {
		var retry string
		if seconds, err := strconv.ParseUint(values[0], 10, 63); err == nil {
			retry = strconv.FormatUint(seconds, 10) + "s"
		} else if date, dateErr := http.ParseTime(values[0]); dateErr == nil {
			retry = date.UTC().Format(time.RFC3339)
		}
		if retry != "" && !strings.Contains(retry, key) {
			message += "; retry after " + retry
		}
	}
	return errors.New(message)
}

func pageRefusalReason(body []byte, key string) string {
	// Decode only the REST error envelope. Keep the CLI independent of server setup.
	var response struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil ||
		strings.TrimSpace(response.Error.Code) == "" || strings.TrimSpace(response.Error.Message) == "" {
		return ""
	}
	detail := response.Error
	var reason strings.Builder
	reason.WriteString("(" + detail.Code + ") " + detail.Message)
	for _, field := range slices.Sorted(maps.Keys(detail.Details)) {
		reason.WriteString(" [" + field + "=" + detail.Details[field] + "]")
	}
	// Redact credentials before escaping terminal controls or truncating text.
	message := safeTerminalText(reason.String(), key)
	if len(message) > maxRefusalMessage {
		const ellipsis = "…"
		message = strings.ToValidUTF8(message[:maxRefusalMessage-len(ellipsis)], "") + ellipsis
	}
	return message
}

func safeTerminalText(raw, key string) string {
	quoted := strconv.Quote(strings.ReplaceAll(raw, key, "[redacted]"))
	return quoted[1 : len(quoted)-1]
}
