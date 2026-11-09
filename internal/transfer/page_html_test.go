// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/store"
)

func importPageHTML(t *testing.T, ts *testSetup, importer *Importer, data *ExportData, opts ImportOptions, format string) (*ImportResult, error) {
	t.Helper()
	if format == "zip" {
		return importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, *data, nil), opts)
	}
	return importer.Import(ts.Ctx, data, opts)
}

func pageHTMLState(t *testing.T, ts *testSetup) string {
	t.Helper()
	var state string
	err := ts.DB.QueryRowContext(ts.Ctx, `SELECT json_array(
 (SELECT json_group_array(json_array(id,title,body,revision,author_id,status)) FROM pages),
 (SELECT json_group_array(json_array(page_id,revision,body,snapshot_json)) FROM page_versions),
 (SELECT COUNT(*) FROM content_provenance), (SELECT COUNT(*) FROM categories),
 (SELECT COUNT(*) FROM media), (SELECT COUNT(*) FROM page_categories))`).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestImportedPageBodiesFollowHTMLPolicy(t *testing.T) {
	for _, format := range []string{"json", "zip"} {
		for _, overwrite := range []bool{false, true} {
			for _, sanitize := range []bool{false, true} {
				for _, block := range []bool{false, true} {
					for _, payload := range []struct {
						name, raw string
						blocked   bool
					}{
						{"script", `<p>Safe</p><script>B2_PRIVATE_BODY()</script>`, true},
						{"encoded-uri", `<p><a href="jav&#x61;script:B2_PRIVATE_BODY()">Safe</a></p>`, false},
						{"editor-formatting", `<p style="text-align: center;"><span style="color: #e03e2d;">Safe</span></p>`, false},
					} {
						t.Run(fmt.Sprintf("%s/overwrite=%v/sanitize=%v/block=%v/%s", format, overwrite, sanitize, block, payload.name), func(t *testing.T) {
							ts := setupTest(t)
							defer ts.Cleanup()
							var original store.Page
							if overwrite {
								original = aiEditablePage(t, ts, "body-policy")
							}
							data := &ExportData{Version: ExportVersion, Pages: []ExportPage{{ID: 1, Title: "Imported", Slug: "body-policy", Body: payload.raw, Status: "draft", AuthorEmail: ts.User.Email}}}
							importer := NewImporter(ts.Queries, ts.DB, slog.Default())
							importer.SetPrincipal(provenance.Principal{UserID: ts.User.ID, Source: "human", Transport: "admin"})
							importer.SetSanitizePageHTML(sanitize)
							importer.SetBlockSuspiciousMarkup(block)
							before := pageHTMLState(t, ts)
							result, err := importPageHTML(t, ts, importer, data, overwriteOptions, format)
							if data.Pages[0].Body != payload.raw {
								t.Fatal("import mutated caller-owned HTML")
							}
							if block && payload.blocked {
								if err == nil || !strings.Contains(err.Error(), "blocked by policy") || result == nil || result.Success || len(result.Errors) != 1 {
									t.Fatalf("policy refusal: result=%+v error=%v", result, err)
								}
								if pageHTMLState(t, ts) != before || result.TotalCreated()+result.TotalUpdated() != 0 || len(result.Notices) != 0 {
									t.Fatal("refused import changed state or reported completed work")
								}
								if strings.Contains(err.Error(), "B2_PRIVATE_BODY") || strings.Contains(result.Errors[0].Message, "B2_PRIVATE_BODY") {
									t.Fatal("refusal exposed imported body")
								}
								return
							}
							if err != nil || result == nil || !result.Success {
								t.Fatalf("ordinary import: result=%+v error=%v", result, err)
							}
							want := payload.raw
							if sanitize {
								want = security.SanitizePageHTML(payload.raw)
								if strings.Contains(want, "B2_PRIVATE_BODY") {
									t.Fatal("fixture no longer distinguishes sanitizer from coarse blocker")
								}
							}
							stored, loadErr := ts.Queries.GetPageBySlug(ts.Ctx, "body-policy")
							wantRevision := int64(1)
							if overwrite {
								wantRevision = original.Revision + 1
								ai, aiErr := ts.Queries.GetPageAIAccess(ts.Ctx, stored.ID)
								if aiErr != nil || !ai.AiEditable || stored.AuthorID != original.AuthorID {
									t.Fatal("overwrite changed owner or AI access", aiErr)
								}
							}
							if loadErr != nil || stored.Body != want || stored.Revision != wantRevision {
								t.Fatalf("stored body/revision mismatch: body=%q revision=%d error=%v", stored.Body, stored.Revision, loadErr)
							}
							version, versionErr := ts.Queries.GetPageVersionByRevision(ts.Ctx, store.GetPageVersionByRevisionParams{PageID: stored.ID, Revision: sql.NullInt64{Int64: stored.Revision, Valid: true}})
							var snapshot struct{ Body string }
							if versionErr != nil || version.Body != want || json.Unmarshal([]byte(version.SnapshotJson.String), &snapshot) != nil || snapshot.Body != want {
								t.Fatal("complete revision retained unnormalized HTML", versionErr)
							}
							actor, actorErr := ts.Queries.GetContentProvenance(ts.Ctx, store.GetContentProvenanceParams{PageID: stored.ID, Revision: stored.Revision})
							if actorErr != nil || actor.UserID.Int64 != ts.User.ID || actor.Source != "human" || actor.Transport != "admin" {
								t.Fatal("import provenance changed", actorErr)
							}
							exported, exportErr := NewExporter(ts.Queries, slog.Default()).Export(ts.Ctx, DefaultExportOptions())
							if exportErr != nil || len(exported.Pages) != 1 || exported.Pages[0].Body != want {
								t.Fatal("export did not consume normalized stored body", exportErr)
							}
							wantNotices := 0
							if sanitize && payload.name != "editor-formatting" {
								wantNotices = 1
							}
							if len(result.Notices) != wantNotices {
								t.Fatalf("removal notices=%d, want %d", len(result.Notices), wantNotices)
							}
							if wantNotices == 1 && (result.Notices[0].ID != stored.Slug || strings.Contains(result.Notices[0].Message, "B2_PRIVATE_BODY")) {
								t.Fatal("removal notice has wrong identity or exposes body")
							}
						})
					}
				}
			}
		}
	}
}

func TestImportHTMLPolicyPreservesSelectionAndRollback(t *testing.T) {
	for _, format := range []string{"json", "zip"} {
		for _, scenario := range []string{"skip", "rename", "deselected", "dry-run", "rollback"} {
			t.Run(format+"/"+scenario, func(t *testing.T) {
				ts := setupTest(t)
				defer ts.Cleanup()
				original := aiEditablePage(t, ts, "body-policy")
				data := &ExportData{Version: ExportVersion, Pages: []ExportPage{{ID: 1, Title: "Imported", Slug: original.Slug, Body: `<p>Safe</p><script>B2_PRIVATE_BODY()</script>`, Status: "draft", AuthorEmail: ts.User.Email}}}
				importer := NewImporter(ts.Queries, ts.DB, slog.Default())
				importer.SetSanitizePageHTML(true)
				opts := overwriteOptions
				switch scenario {
				case "skip":
					opts.ConflictStrategy = ConflictSkip
				case "rename":
					opts.ConflictStrategy = ConflictRename
				case "deselected":
					opts.ImportPages = false
					importer.SetBlockSuspiciousMarkup(true)
				case "dry-run":
					opts.DryRun = true
				case "rollback":
					_, err := ts.DB.ExecContext(ts.Ctx, `CREATE TRIGGER b2_provenance_fault BEFORE INSERT ON content_provenance BEGIN SELECT RAISE(ABORT, 'B2 provenance fault'); END`)
					if err != nil {
						t.Fatal(err)
					}
				}
				before := pageHTMLState(t, ts)
				result, err := importPageHTML(t, ts, importer, data, opts, format)
				if scenario == "rollback" {
					if err == nil || !strings.Contains(err.Error(), "B2 provenance fault") || result == nil || result.Success || result.TotalCreated()+result.TotalUpdated() != 0 {
						t.Fatalf("late failure did not roll back: %+v %v", result, err)
					}
				} else if err != nil || result == nil || !result.Success {
					t.Fatalf("selection control failed: %+v %v", result, err)
				}
				if scenario == "rename" {
					created, loadErr := ts.Queries.GetPageByID(ts.Ctx, result.IDMaps["pages"][1])
					if loadErr != nil || created.ID == original.ID || created.Body != "<p>Safe</p>" || len(result.Notices) != 1 {
						t.Fatal("rename did not sanitize its new page", loadErr)
					}
				} else if pageHTMLState(t, ts) != before || len(result.Notices) != 0 {
					t.Fatal("non-write or rollback changed state/reported removals")
				}
				unchanged, loadErr := ts.Queries.GetPageByID(ts.Ctx, original.ID)
				if loadErr != nil || !reflect.DeepEqual(unchanged, original) {
					t.Fatal("original page changed", loadErr)
				}
			})
		}
	}
}

func TestImportHTMLRefusalPrecedesZIPExtraction(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprint(dryRun), func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			const medium = "550e8400-e29b-41d4-a716-446655440000"
			data := ExportData{Version: ExportVersion,
				Media: []ExportMedia{{UUID: medium, Filename: "report.pdf", UploadedBy: ts.User.Email, Size: 6, FilePath: "media/originals/" + medium + "/report.pdf"}},
				Pages: []ExportPage{{ID: 1, Title: "Blocked", Slug: "blocked", Body: `<img alt=">"onerror="B2_PRIVATE_BODY()">`, Status: "draft", AuthorEmail: ts.User.Email}},
			}
			archive := mediaArchiveBytes(t, data, []mediaArchiveEntry{{name: "media/originals/" + medium + "/report.pdf", body: []byte("report")}})
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			root := filepath.Join(t.TempDir(), "uploads-not-created")
			importer.SetUploadDir(root)
			importer.SetBlockSuspiciousMarkup(true)
			importer.SetSanitizePageHTML(true)
			preview, err := importer.ValidateZipBytes(ts.Ctx, archive)
			if err != nil || preview == nil || !preview.Valid {
				t.Fatalf("option-neutral upload validation blocked deselectable pages: %+v %v", preview, err)
			}
			before := pageHTMLState(t, ts)
			opts := ImportOptions{ImportPages: true, ImportMedia: true, ImportMediaFiles: true, DryRun: dryRun}
			result, err := importer.ImportFromZipBytes(ts.Ctx, archive, opts)
			if err == nil || !strings.Contains(err.Error(), "blocked by policy") || result == nil || result.Success {
				t.Fatalf("ZIP policy preflight missing: %+v %v", result, err)
			}
			if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) || pageHTMLState(t, ts) != before {
				t.Fatal("refused ZIP wrote files or content", statErr)
			}
			// Replacing only the rejected body permits the same media restore.
			// Local download links survive the sanitizer, and a dry run stays dry.
			data.Pages[0].Body = `<p>Safe</p><a href="/uploads/originals/` + medium + `/report.pdf">Report</a>`
			archive = mediaArchiveBytes(t, data, []mediaArchiveEntry{{name: "media/originals/" + medium + "/report.pdf", body: []byte("report")}})
			result, err = importer.ImportFromZipBytes(ts.Ctx, archive, opts)
			if err != nil || result == nil || !result.Success || len(result.Notices) != 0 {
				t.Fatalf("normal media import failed after refusal: %+v %v", result, err)
			}
			if dryRun {
				if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) || pageHTMLState(t, ts) != before {
					t.Fatal("normal dry run wrote files or content", statErr)
				}
			} else {
				stored, loadErr := ts.Queries.GetPageBySlug(ts.Ctx, "blocked")
				contents, readErr := os.ReadFile(filepath.Join(root, "originals", medium, "report.pdf"))
				if loadErr != nil || readErr != nil || stored.Body != data.Pages[0].Body || string(contents) != "report" {
					t.Fatal("media/link recovery failed", loadErr, readErr)
				}
			}
		})
	}
}
