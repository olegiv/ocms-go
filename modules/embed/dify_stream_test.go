// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/modules/embed/providers"
)

// Exercise the actual rendered widget with fragmented byte streams and a minimal DOM.
func TestDifyWidgetStreaming(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required to execute widget JavaScript")
	}
	html := string(providers.NewDify().RenderBody(map[string]string{
		"api_endpoint": "https://api.dify.ai/v1", "api_key": "app-test",
	}, providers.RenderContext{}))
	_, script, ok := strings.Cut(html, "<script>")
	if !ok {
		t.Fatal("widget script missing")
	}
	script, _, ok = strings.Cut(script, "</script>")
	if !ok {
		t.Fatal("widget script terminator missing")
	}
	cmd := exec.Command(node, "testdata/dify_stream.cjs")
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("widget streaming tests: %v\n%s", err, output)
	}
}
