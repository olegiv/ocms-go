// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite

import (
	"context"
	"errors"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// ErrTranslationExists does not disclose the identity of a conflicting sibling.
var ErrTranslationExists = errors.New("translation_exists")

// LinkTranslation validates and links within the page write transaction. Every
// existing sibling is tracked because component membership changes for them too.
func (t *Tx) LinkTranslation(ctx context.Context, sourceID int64, langCode string, targetID int64) error {
	language, err := t.Q.GetLanguageByCode(ctx, langCode)
	if err != nil {
		return err
	}
	if !language.IsActive || !util.IsRoutableLanguageCode(language.Code) {
		return refuse("target language is not active and routable")
	}
	target, err := t.Q.GetPageByID(ctx, targetID)
	if err != nil {
		return err
	}
	if target.LanguageCode != langCode || sourceID == targetID {
		return refuse("invalid translation target")
	}
	source, err := t.Q.GetPageByID(ctx, sourceID)
	if err != nil {
		return err
	}
	sourceMembers, err := t.Q.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{EntityType: "page", SourceEntityID: sourceID})
	if err != nil {
		return err
	}
	targetMembers, err := t.Q.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{EntityType: "page", SourceEntityID: targetID})
	if err != nil {
		return err
	}
	languages := map[string]bool{source.LanguageCode: true}
	for _, member := range sourceMembers {
		languages[member.LanguageCode] = true
	}
	if languages[target.LanguageCode] {
		return ErrTranslationExists
	}
	languages[target.LanguageCode] = true
	for _, member := range targetMembers {
		if languages[member.LanguageCode] {
			return ErrTranslationExists
		}
		languages[member.LanguageCode] = true
	}
	if err = t.TrackComponent(ctx, sourceID); err != nil {
		return err
	}
	if err = t.TrackComponent(ctx, targetID); err != nil {
		return err
	}
	_, err = t.Q.CreateTranslation(ctx, store.CreateTranslationParams{EntityType: "page", EntityID: sourceID, LanguageID: language.ID, TranslationID: targetID, CreatedAt: time.Now().UTC()})
	return err
}
