// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package review presents recorded evidence without promoting agent claims to facts.
package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

type Reference struct {
	PageID   int64  `json:"page_id"`
	Revision int64  `json:"revision"`
	Role     string `json:"role"`
}
type Basis struct {
	SourcePageID   int64  `json:"source_page_id"`
	SourceRevision int64  `json:"source_revision"`
	Fingerprint    string `json:"fingerprint"`
}
type Validation struct {
	Issues                []draft.Issue `json:"issues"`
	References            []Reference   `json:"references"`
	Links                 []draft.Link  `json:"links"`
	TranslationBasis      *Basis        `json:"translation_basis"`
	PromptContractVersion string        `json:"prompt_contract_version"`
	SiteURL               string        `json:"site_url"`
}
type Report struct {
	Agent            draft.AgentReport `json:"agent"`
	MCPClientName    string            `json:"mcp_client_name"`
	MCPClientVersion string            `json:"mcp_client_version"`
}
type Claim struct {
	Index     int64
	Text      string
	Checked   bool
	CheckedBy string
	CheckedAt int64
}
type Translation struct {
	Language, Title, Status, Freshness string
	PageID, Revision                   int64
}
type Review struct {
	Fact                     store.ContentProvenance
	ActorName, RegisteredApp string
	Validated                Validation
	Reported                 Report
	Claims                   []Claim
	Translations             []Translation
	Links                    []store.ListPagePreviewLinksRow
}

func decodeReport(raw string) (Report, error) {
	var wrapper struct {
		Agent json.RawMessage `json:"agent"`
	}
	var result Report
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return result, err
	}
	var err error
	if wrapper.Agent != nil {
		err = json.Unmarshal([]byte(raw), &result)
	} else {
		err = json.Unmarshal([]byte(raw), &result.Agent)
	}
	return result, err
}

func Load(ctx context.Context, q *store.Queries, page store.Page, viewer int64) (Review, error) {
	if err := pagewrite.RequireEditor(ctx, q, viewer); err != nil {
		return Review{}, err
	}
	fact, err := q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: page.ID, Revision: page.Revision})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Review{}, err
	}
	result := Review{Fact: fact}
	if fact.ID != 0 {
		if err = json.Unmarshal([]byte(fact.ValidatedJson), &result.Validated); err != nil {
			return Review{}, err
		}
		if result.Reported, err = decodeReport(fact.ReportedJson); err != nil {
			return Review{}, err
		}
		if fact.UserID.Valid {
			if user, e := q.GetUserByID(ctx, fact.UserID.Int64); e == nil {
				result.ActorName = user.Name
			}
		}
		if fact.OauthClientID != "" {
			if client, e := q.GetOAuthClient(ctx, fact.OauthClientID); e == nil {
				result.RegisteredApp = client.Name
			}
		}
		if fact.ApiKeyID.Valid {
			if key, e := q.GetAPIKeyByID(ctx, fact.ApiKeyID.Int64); e == nil {
				result.RegisteredApp = key.Name
			}
		}
		checks, e := q.ListClaimChecks(ctx, fact.ID)
		if e != nil {
			return Review{}, e
		}
		for i, text := range result.Reported.Agent.ClaimsToVerify.Value {
			claim := Claim{Index: int64(i), Text: text}
			for _, check := range checks {
				if check.ClaimIndex == claim.Index {
					claim.Checked = true
					claim.CheckedBy = check.CheckedByName
					claim.CheckedAt = check.CheckedAt
				}
			}
			result.Claims = append(result.Claims, claim)
		}
	}
	result.Translations, err = Translations(ctx, q, page)
	if err != nil {
		return Review{}, err
	}
	result.Links, err = q.ListPagePreviewLinks(ctx, store.ListPagePreviewLinksParams{PageID: page.ID, ExpiresAt: time.Now().Unix()})
	return result, err
}

// SetClaim records an editorial acknowledgment, without creating or changing content evidence.
func SetClaim(ctx context.Context, q *store.Queries, viewer, pageID, revision, index int64, checked bool) error {
	_, err := pagewrite.InQueriesTx(ctx, q, provenance.Principal{UserID: viewer, Source: "human", Transport: "editorial_review"}, func(work *pagewrite.Tx) error {
		if err := pagewrite.RequireEditor(ctx, work.Q, viewer); err != nil {
			return err
		}
		page, err := work.Q.GetPageByID(ctx, pageID)
		if err != nil {
			return err
		}
		if page.Revision != revision {
			return &pagewrite.Conflict{CurrentRevision: page.Revision}
		}
		fact, err := work.Q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: pageID, Revision: revision})
		if err != nil {
			return err
		}
		report, err := decodeReport(fact.ReportedJson)
		if err != nil {
			return err
		}
		if index < 0 || index >= int64(len(report.Agent.ClaimsToVerify.Value)) || index >= 50 {
			return errors.New("claim unavailable")
		}
		if checked {
			return work.Q.CheckClaim(ctx, store.CheckClaimParams{ProvenanceID: fact.ID, ClaimIndex: index, CheckedBy: sql.NullInt64{Int64: viewer, Valid: true}, CheckedAt: time.Now().Unix()})
		}
		return work.Q.UncheckClaim(ctx, store.UncheckClaimParams{ProvenanceID: fact.ID, ClaimIndex: index})
	})
	return err
}

func Translations(ctx context.Context, q *store.Queries, page store.Page) ([]Translation, error) {
	members, err := q.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{EntityType: "page", SourceEntityID: page.ID})
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{page.LanguageCode: page.ID}
	for _, member := range members {
		ids[member.LanguageCode] = member.EntityID
	}
	languages, err := q.ListActiveLanguages(ctx)
	if err != nil {
		return nil, err
	}
	var result []Translation
	for _, language := range languages {
		item := Translation{Language: language.Code, Status: "missing", Freshness: "not_tracked"}
		if id := ids[language.Code]; id != 0 {
			p, e := q.GetPageByID(ctx, id)
			if e != nil {
				return nil, e
			}
			item.PageID = p.ID
			item.Revision = p.Revision
			item.Title = p.Title
			item.Status = p.Status
			if access, e := q.GetPageAIAccess(ctx, p.ID); e == nil && access.AiEditable && p.Status == "draft" {
				item.Status = "ai_draft"
			}
			fact, e := q.GetLatestTranslationProvenance(ctx, store.GetLatestTranslationProvenanceParams{PageID: p.ID, Revision: p.Revision})
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			if e == nil {
				var v Validation
				if e = json.Unmarshal([]byte(fact.ValidatedJson), &v); e != nil {
					return nil, e
				}
				if v.TranslationBasis != nil {
					basis := v.TranslationBasis
					linked := false
					for _, id := range ids {
						if id == basis.SourcePageID {
							linked = true
						}
					}
					if linked {
						source, e := q.GetPageByID(ctx, basis.SourcePageID)
						switch {
						case e != nil:
							item.Freshness = "unavailable"
						case provenance.PageFingerprint(source) == basis.Fingerprint:
							item.Freshness = "current"
						default:
							item.Freshness = "stale"
							item.Status = "stale"
						}
					}
				}
			}
		}
		result = append(result, item)
	}
	return result, nil
}
