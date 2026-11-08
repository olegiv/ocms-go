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
	raw := link.URL.Reveal()
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
	if err = json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded, link) {
		t.Fatal("typed issuance response no longer round trips")
	}
	if _, err = f.svc.Resolve(t.Context(), bearer); err != nil {
		t.Fatal("issued bearer no longer resolves its snapshot")
	}
}

func TestShareLinkRedactsWrappedDiagnostics(t *testing.T) {
	f := setup(t)
	link := f.issue(t)
	bearer := token(t, link)
	wrapped := struct {
		url  preview.ShareURL
		link preview.Link
	}{link.URL, link}

	check := func(description, output string) {
		t.Helper()
		if strings.Contains(output, bearer) {
			t.Errorf("%s exposed the share bearer", description)
		}
	}
	for _, value := range []any{link.URL, link} {
		check(fmt.Sprintf("%%p on %T", value), fmt.Sprintf("%p", value))
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		check("private fields with "+format, fmt.Sprintf(format, wrapped))
	}
	var output bytes.Buffer
	slog.New(slog.NewTextHandler(&output, nil)).Info("share issued", "receipt", wrapped)
	check("text slog with private fields", output.String())
	check("slog.Value.String with private fields", slog.AnyValue(wrapped).String())
}

func TestShareURLJSONPreservesZeroValueAndCopies(t *testing.T) {
	var value preview.ShareURL
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != `""` || value.Reveal() != "" {
		t.Fatal("zero share URL lost its empty string representation")
	}
	if err := json.Unmarshal([]byte(`"fixture-url"`), &value); err != nil {
		t.Fatal(err)
	}
	copyOfURL := value
	if err := json.Unmarshal([]byte(`null`), &value); err != nil || value.Reveal() != "fixture-url" {
		t.Fatal("JSON null replaced a share URL")
	}
	if err := json.Unmarshal([]byte(`1`), &value); err == nil || value.Reveal() != "fixture-url" {
		t.Fatal("invalid JSON replaced a share URL")
	}
	if err := json.Unmarshal([]byte(`""`), &value); err != nil || value.Reveal() != "" || copyOfURL.Reveal() != "fixture-url" {
		t.Fatal("decoding did not replace only the target share URL")
	}
}
