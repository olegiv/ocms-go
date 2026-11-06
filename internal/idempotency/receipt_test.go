// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package idempotency_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/idempotency"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

type extendedReceipt struct {
	idempotency.Page
	URL string `json:"url"`
}

type marshalingReceipt struct{ idempotency.Page }

func (marshalingReceipt) MarshalJSON() ([]byte, error) {
	return []byte(`{"url":"https://example.test/preview/share#test-only-bearer"}`), nil
}

func TestStoreAcceptsOnlyPageReceipts(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q, now := store.New(db), time.Now().UTC().Truncate(time.Second)
	user, err := q.CreateUser(t.Context(), store.CreateUserParams{Email: "receipt@example.test", Name: "Editor", Role: "editor", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	full := idempotency.Page{ID: 7, Revision: 3, Title: "Saved title", Slug: "saved", Body: "<p>Saved body</p>", Summary: "Summary", Status: "draft", PageType: "page", AuthorID: user.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
		Author: &idempotency.Author{ID: user.ID, Name: user.Name, Email: user.Email}, Categories: []idempotency.Category{{ID: 2, Name: "Category", Slug: "category", Description: "Description"}}, Tags: []idempotency.Tag{{ID: 4, Name: "Tag", Slug: "tag"}}}
	compact := idempotency.Page{ID: full.ID, Revision: full.Revision, Slug: full.Slug, LanguageCode: full.LanguageCode, Status: full.Status}
	oversized := full
	oversized.Body = strings.Repeat("x", 256*1024)
	for _, test := range []struct {
		name   string
		result idempotency.Receipt
		valid  bool
	}{
		{"full value", full, true},
		{"full pointer", &full, true},
		{"compact agent receipt", &compact, true},
		{"nil interface", nil, false},
		{"nil page", (*idempotency.Page)(nil), false},
		{"embedded share URL", extendedReceipt{Page: compact, URL: "https://example.test/preview/share#test-only-bearer"}, false},
		{"embedded pointer", &extendedReceipt{Page: compact, URL: "https://example.test/preview/share#test-only-bearer"}, false},
		{"custom JSON", marshalingReceipt{Page: compact}, false},
		{"oversized page", oversized, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, requestErr := idempotency.New(user.ID, "save", uuid.NewString(), map[string]string{"title": "Saved title"})
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			storeErr := idempotency.Store(t.Context(), q, request, "page", full.ID, full.Revision, test.result, now)
			record, lookupErr := q.GetIdempotencyKey(t.Context(), store.GetIdempotencyKeyParams{UserID: user.ID, Tool: request.Tool, RequestID: request.ID})
			if !test.valid {
				if storeErr == nil || !errors.Is(lookupErr, sql.ErrNoRows) {
					t.Fatal("unapproved or invalid receipt reached replay storage")
				}
				return
			}
			if storeErr != nil || lookupErr != nil {
				t.Fatalf("approved receipt refused: store=%v lookup=%v", storeErr, lookupErr)
			}
			encoded, encodeErr := json.Marshal(test.result)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			if !record.ResultJson.Valid || record.ResultJson.String != string(encoded) || record.EntityID != full.ID || record.EntityRevision != full.Revision {
				t.Fatal("approved receipt content or identity changed")
			}
		})
	}
}

func TestReceiptStoreContract(t *testing.T) {
	receiptType := reflect.TypeFor[idempotency.Receipt]()
	if reflect.TypeOf(idempotency.Store).In(6) != receiptType || receiptType.NumMethod() != 1 || receiptType.Method(0).PkgPath == "" {
		t.Fatal("Store must require a receipt sealed by an unexported method")
	}
	// Changes to replay JSON require an explicit field review. In particular,
	// no arbitrary map, interface, raw JSON, or credential type can be added.
	shapes := map[reflect.Type]string{
		reflect.TypeFor[idempotency.Page]():     "revision id title slug body summary status page_type author_id language_code created_at updated_at published_at featured_image_id hide_featured_image exclude_from_lists meta_title meta_description meta_keywords og_image_id no_index no_follow canonical_url scheduled_at video_url video_title author categories tags",
		reflect.TypeFor[idempotency.Author]():   "id name email",
		reflect.TypeFor[idempotency.Category](): "id name slug description",
		reflect.TypeFor[idempotency.Tag]():      "id name slug",
	}
	for shape, fields := range shapes {
		if shape.Implements(reflect.TypeFor[json.Marshaler]()) || reflect.PointerTo(shape).Implements(reflect.TypeFor[json.Marshaler]()) {
			t.Errorf("%s must use its reviewed JSON fields, not a custom serializer", shape.Name())
		}
		var actual []string
		for index := range shape.NumField() {
			field := shape.Field(index)
			actual = append(actual, strings.Split(field.Tag.Get("json"), ",")[0])
			fieldType := field.Type
			for fieldType.Kind() == reflect.Pointer || fieldType.Kind() == reflect.Slice {
				fieldType = fieldType.Elem()
			}
			if _, approved := shapes[fieldType]; approved {
				continue
			}
			switch fieldType {
			case reflect.TypeFor[string](), reflect.TypeFor[int64](), reflect.TypeFor[bool](), reflect.TypeFor[time.Time]():
			default:
				t.Errorf("unapproved replay field type: %s.%s", shape.Name(), field.Name)
			}
		}
		if !slices.Equal(actual, strings.Fields(fields)) {
			t.Errorf("%s replay field allowlist changed: %v", shape.Name(), actual)
		}
	}
}
