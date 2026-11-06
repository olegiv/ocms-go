// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/preview"
)

func TestShareLinkRedactsLogsButPreservesIssuanceJSON(t *testing.T) {
	f := setup(t)
	link := f.issue(t)
	raw := string(link.URL)
	bearer := token(t, link)
	if reflect.TypeOf(link.URL) != reflect.TypeFor[preview.ShareURL]() {
		t.Fatal("issuance URL lost its redacting type")
	}
	for _, value := range []any{link.URL, &link.URL, link, &link} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%#x", "%80s", "%.8s"} {
			formatted := fmt.Sprintf(format, value)
			if !strings.Contains(formatted, "REDACTED") || strings.Contains(formatted, bearer) || strings.Contains(formatted, fmt.Sprintf("%x", bearer)) || strings.Contains(formatted, fmt.Sprintf("%X", bearer)) {
				t.Errorf("%T exposed the share bearer with format %q", value, format)
			}
		}
	}
	for _, handlerType := range []string{"text", "json"} {
		var output bytes.Buffer
		var handler slog.Handler = slog.NewTextHandler(&output, nil)
		if handlerType == "json" {
			handler = slog.NewJSONHandler(&output, nil)
		}
		slog.New(handler).Info("share issued", "url", link.URL, "url_pointer", &link.URL, "link", link, "link_pointer", &link)
		if strings.Contains(output.String(), bearer) || !strings.Contains(output.String(), "REDACTED") {
			t.Errorf("%s logging did not redact the issuance result", handlerType)
		}
		if !strings.Contains(output.String(), "page_id") || !strings.Contains(output.String(), "revision") {
			t.Errorf("%s logging discarded useful receipt context", handlerType)
		}
	}
	encoded, err := json.Marshal(link)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		URL string `json:"url"`
	}
	if err = json.Unmarshal(encoded, &response); err != nil || response.URL != raw {
		t.Fatal("issuance JSON lost the usable share URL")
	}
	var decoded preview.Link
	if err = json.Unmarshal(encoded, &decoded); err != nil || decoded != link {
		t.Fatal("typed issuance response no longer round trips")
	}
	if _, err = f.svc.Resolve(t.Context(), bearer); err != nil {
		t.Fatal("issued bearer no longer resolves its snapshot")
	}
}
