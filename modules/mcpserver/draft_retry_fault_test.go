// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestCommittedDraftReceiptReadFaultIsRetryable(t *testing.T) {
	env, id := draftKeyFixture(t)
	previous := slog.Default()
	slog.SetDefault(slog.New(env.logs))
	t.Cleanup(func() { slog.SetDefault(previous) })
	svc, actor, err := env.module.authoringService(t.Context(), &toolCall{identity: id, settings: env.module.currentSettings()})
	if err != nil {
		t.Fatal(err)
	}
	input := authoring.CreateInput{RequestID: uuid.NewString(),
		Draft: draft.PageDraft{Title: "Committed draft", Summary: "Review", BodyMarkdown: "Safe body."}}
	saved, err := svc.Create(t.Context(), actor, input)
	if err != nil || saved.PageID <= 0 {
		t.Fatalf("create draft: %v", err)
	}

	var fault error
	withTableUnavailable(t, env.db, "content_provenance", func() {
		_, fault = svc.Create(t.Context(), actor, input)
	})
	requireDraftRetryCode(t, fault)
	logs := env.logs.find("failed to read draft receipt")
	if len(logs) != 1 {
		t.Fatalf("want one receipt fault diagnostic: %s", env.logs.text())
	}
	cause, _ := recordAttr(logs[0], "error")
	pageID, _ := recordAttr(logs[0], "page_id")
	revision, _ := recordAttr(logs[0], "revision")
	if !strings.Contains(cause.String(), "no such table") || pageID.Int64() != saved.PageID || revision.Int64() != saved.Revision {
		t.Fatalf("receipt diagnostic omitted the cause or receipt identity: %v", logs[0])
	}
	assertDraftPolicyRows(t, env, 1)
	replayed, err := svc.Create(t.Context(), actor, input)
	if err != nil || replayed.PageID != saved.PageID || replayed.Revision != saved.Revision {
		t.Fatalf("retry did not return the original receipt: %+v, %v", replayed, err)
	}
	assertDraftPolicyRows(t, env, 1)
}

func TestDraftAccountRecheckStorageFaultsAreRetryable(t *testing.T) {
	for _, failingRead := range []int{1, 2} {
		name := "role read"
		if failingRead == 2 {
			name = "account snapshot read"
		}
		t.Run(name, func(t *testing.T) {
			env, id := draftKeyFixture(t)
			previous := slog.Default()
			slog.SetDefault(slog.New(env.logs))
			t.Cleanup(func() { slog.SetDefault(previous) })
			svc, actor, err := env.module.authoringService(t.Context(), &toolCall{identity: id, settings: env.module.currentSettings()})
			if err != nil {
				t.Fatal(err)
			}
			read := 0
			injected := errors.New("injected account storage failure")
			db := openDraftFaultDB(t, env, "immediate", func(query string) error {
				if strings.Contains(query, "FROM users WHERE id") {
					read++
					if read == failingRead {
						return injected
					}
				}
				return nil
			})
			svc.Pages = pages.NewService(db, store.New(db), nil, nil, pages.Policy{})
			input := authoring.CreateInput{RequestID: uuid.NewString(),
				Draft: draft.PageDraft{Title: "Account recheck", Summary: "Review", BodyMarkdown: "Safe body."}}
			_, err = svc.Create(t.Context(), actor, input)
			if !errors.Is(err, injected) {
				t.Fatalf("did not hit the intended account read: %v", err)
			}
			requireDraftRetryCode(t, err)
			logs := env.logs.find("failed to re-check draft write credential")
			if len(logs) != 1 {
				t.Fatalf("want one account fault diagnostic: %s", env.logs.text())
			}
			cause, _ := recordAttr(logs[0], "error")
			check, _ := recordAttr(logs[0], "check")
			userID, _ := recordAttr(logs[0], "user_id")
			if cause.String() != injected.Error() || check.String() != "account" || userID.Int64() != env.userID {
				t.Fatalf("account diagnostic omitted cause or user ID: %v", logs[0])
			}
			assertDraftPolicyRows(t, env, 0)
			if saved, saveErr := svc.Create(t.Context(), actor, input); saveErr != nil || saved.PageID <= 0 {
				t.Fatalf("same draft call after recovery: %v", saveErr)
			}
			assertDraftPolicyRows(t, env, 1)
			if _, err = env.db.ExecContext(t.Context(), "UPDATE users SET role='public' WHERE id=?", env.userID); err != nil {
				t.Fatal(err)
			}
			_, err = svc.Create(t.Context(), actor, input)
			denied, _ := toToolError(err)
			if denied == nil || denied.Code != codeForbidden || denied.Retryable {
				t.Fatalf("demoted account answered %+v, want non-retryable forbidden", denied)
			}
			if len(env.logs.find("failed to re-check draft write credential")) != 1 {
				t.Fatal("recovery or demotion logged another account fault")
			}
			assertDraftPolicyRows(t, env, 1)
		})
	}
}
