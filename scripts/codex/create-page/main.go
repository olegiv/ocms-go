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
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/contentpath"
)

const (
	maxKeyBytes      = 4096
	maxPageBytes     = 1 << 20 // Huma rejects JSON bodies at or above 1 MiB.
	maxDocumentBytes = 8 << 20
	requestTimeout   = 30 * time.Second
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
	encoder := json.NewEncoder(output)
	for i, page := range pages {
		summary, createErr := createPage(ctx, client, endpoint, key, page)
		if createErr != nil {
			return fmt.Errorf("page %d of %d: %w", i+1, len(pages), createErr)
		}
		if err = encoder.Encode(summary); err != nil {
			return fmt.Errorf("page %d was created but its result could not be written: %w", i+1, err)
		}
	}
	return nil
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
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes+1))
	closeErr := resp.Body.Close()
	// Do not relay arbitrary server diagnostics, which can reflect credentials.
	if resp.StatusCode != http.StatusCreated {
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout {
			return pageSummary{}, fmt.Errorf("page API returned HTTP %d; verify whether it was saved before retrying", resp.StatusCode)
		}
		return pageSummary{}, fmt.Errorf("page API returned HTTP %d", resp.StatusCode)
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
