// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestGetMenuLinksPreservesSourceLangcode(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE menu_link_content (
			id INTEGER PRIMARY KEY,
			uuid TEXT NOT NULL
		);
		CREATE TABLE menu_link_content_data (
			id INTEGER PRIMARY KEY,
			title TEXT,
			menu_name TEXT,
			langcode TEXT,
			link__uri TEXT,
			parent TEXT,
			weight INTEGER,
			enabled INTEGER,
			default_langcode INTEGER
		);
		INSERT INTO menu_link_content (id, uuid) VALUES (1, 'link-fr');
		INSERT INTO menu_link_content_data
			(id, title, menu_name, langcode, link__uri, parent, weight, enabled, default_langcode)
		VALUES (1, 'Current', 'main', 'fr', 'internal:/topics/current', NULL, 0, 1, 1);
	`); err != nil {
		t.Fatal(err)
	}

	reader := &Reader{db: db, schema: Schema{HasMenuLinks: true}}
	links, err := reader.GetMenuLinks(context.Background())
	if err != nil {
		t.Fatalf("GetMenuLinks() error = %v", err)
	}
	if len(links) != 1 || links[0].Langcode != "fr" {
		t.Fatalf("GetMenuLinks() = %+v, want one source-language fr link", links)
	}
}

func TestTermParentsChoosesFirstDeltaAndReportsAdditionalParents(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE taxonomy_term_field_data (
			tid INTEGER PRIMARY KEY, langcode TEXT, revision_id INTEGER, default_langcode INTEGER
		);
		CREATE TABLE taxonomy_term__parent (
			entity_id INTEGER, parent_target_id INTEGER, langcode TEXT,
			revision_id INTEGER, deleted INTEGER, delta INTEGER
		);
		INSERT INTO taxonomy_term_field_data VALUES (10, 'en', 7, 1);
		INSERT INTO taxonomy_term__parent VALUES
			(10, 20, 'en', 7, 0, 0),
			(10, 30, 'en', 7, 0, 1),
			(10, 40, 'en', 6, 0, 2),
			(10, 50, 'fr', 7, 0, 3),
			(10, 60, 'en', 7, 1, 4);
	`); err != nil {
		t.Fatal(err)
	}
	reader := &Reader{db: db, schema: Schema{HasTermData: true, HasTermPar: true}}
	parents, err := reader.termParents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if parents[10] != 20 {
		t.Fatalf("chosen parent = %d, want delta-zero parent 20", parents[10])
	}
	warnings := reader.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "additional parent 30") {
		t.Fatalf("warnings = %v, want one discarded active current parent", warnings)
	}
}

func TestTermParentsNeverPromotesActiveNonZeroDelta(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE taxonomy_term_field_data (
			tid INTEGER PRIMARY KEY, langcode TEXT, revision_id INTEGER, default_langcode INTEGER
		);
		CREATE TABLE taxonomy_term__parent (
			entity_id INTEGER, parent_target_id INTEGER, langcode TEXT,
			revision_id INTEGER, deleted INTEGER, delta INTEGER
		);
		INSERT INTO taxonomy_term_field_data VALUES (10, 'en', 7, 1);
		INSERT INTO taxonomy_term__parent VALUES
			(10, 20, 'en', 7, 1, 0),
			(10, 30, 'en', 7, 0, 1);
	`); err != nil {
		t.Fatal(err)
	}
	reader := &Reader{db: db, schema: Schema{HasTermData: true, HasTermPar: true}}
	parents, err := reader.termParents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := parents[10]; exists {
		t.Fatalf("active delta-one parent was promoted: %v", parents)
	}
	warnings := reader.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "additional parent 30 at delta 1") {
		t.Fatalf("warnings = %v, want discarded delta-one warning", warnings)
	}
}

func TestNodeFieldsUseSourceLanguageCurrentRevisionAndActiveRows(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE node_field_data (
			nid INTEGER, vid INTEGER, type TEXT, langcode TEXT, title TEXT, status INTEGER,
			uid INTEGER, created INTEGER, changed INTEGER, default_langcode INTEGER
		);
		CREATE TABLE node__body (
			entity_id INTEGER, revision_id INTEGER, langcode TEXT, deleted INTEGER, delta INTEGER,
			body_value TEXT, body_summary TEXT, body_format TEXT
		);
		CREATE TABLE node__field_image (
			entity_id INTEGER, revision_id INTEGER, langcode TEXT, deleted INTEGER, delta INTEGER,
			field_image_target_id INTEGER
		);
		CREATE TABLE node__field_tags (
			entity_id INTEGER, revision_id INTEGER, langcode TEXT, deleted INTEGER, delta INTEGER,
			field_tags_target_id INTEGER
		);
		CREATE TABLE taxonomy_term_field_data (tid INTEGER, default_langcode INTEGER);
		INSERT INTO node_field_data VALUES (1, 7, 'page', 'en', 'Current', 1, 1, 1, 1, 1);
		INSERT INTO node__body VALUES
			(1, 6, 'en', 0, 0, 'stale body', '', 'basic_html'),
			(1, 7, 'en', 1, 0, 'deleted body', '', 'basic_html'),
			(1, 7, 'fr', 0, 0, 'translated body', '', 'basic_html'),
			(1, 7, 'en', 0, 0, 'current body', 'current summary', 'basic_html');
		INSERT INTO node__field_image VALUES
			(1, 6, 'en', 0, 0, 60), (1, 7, 'en', 1, 0, 70),
			(1, 7, 'fr', 0, 0, 80), (1, 7, 'en', 0, 0, 90);
		INSERT INTO taxonomy_term_field_data VALUES (10, 1), (20, 1), (30, 1), (40, 1);
		INSERT INTO node__field_tags VALUES
			(1, 6, 'en', 0, 0, 10), (1, 7, 'en', 1, 1, 20),
			(1, 7, 'fr', 0, 2, 30), (1, 7, 'en', 0, 3, 40);
	`); err != nil {
		t.Fatal(err)
	}
	reader := &Reader{db: db, schema: Schema{HasNodeBody: true, HasNodeImage: true}}
	nodes, err := reader.GetNodes(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || !nodes[0].Body.Valid || nodes[0].Body.String != "current body" {
		t.Fatalf("GetNodes() = %+v, want only current active English body", nodes)
	}
	images, err := reader.NodeImages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[1] != 90 {
		t.Fatalf("NodeImages() = %v, want current active English file 90", images)
	}
	terms := make(map[int64][]int64)
	query := buildNodeTermsQuery(taxonomyRefField{
		Table: "`node__field_tags`", Column: "field_tags_target_id",
	}, "`node_field_data`", "`taxonomy_term_field_data`")
	if err := reader.collectNodeTerms(context.Background(), query, terms,
		make(map[int64]map[int64]bool)); err != nil {
		t.Fatal(err)
	}
	if got := terms[1]; len(got) != 1 || got[0] != 40 {
		t.Fatalf("node terms = %v, want only current active English term 40", terms)
	}
}

func newAltTextReader(t *testing.T, schema Schema) (*Reader, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE node_field_data (
			nid INTEGER, vid INTEGER, langcode TEXT, default_langcode INTEGER
		);
		CREATE TABLE node__field_image (
			entity_id INTEGER, revision_id INTEGER, langcode TEXT, deleted INTEGER, delta INTEGER,
			field_image_target_id INTEGER, field_image_alt TEXT
		);
		CREATE TABLE media_field_data (
			mid INTEGER, vid INTEGER, langcode TEXT, default_langcode INTEGER
		);
		CREATE TABLE media__field_media_image (
			entity_id INTEGER, revision_id INTEGER, langcode TEXT, deleted INTEGER, delta INTEGER,
			field_media_image_target_id INTEGER, field_media_image_alt TEXT
		);
	`); err != nil {
		t.Fatal(err)
	}
	return &Reader{db: db, schema: schema}, db
}

func TestFileAltTextNodeSelectionIsIndependentOfRowOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values string
	}{
		{"higher owner inserted first", `(20, 7, 'en', 0, 0, 7, 'higher node'),
			(10, 7, 'en', 0, 0, 7, 'lower node')`},
		{"lower owner inserted first", `(10, 7, 'en', 0, 0, 7, 'lower node'),
			(20, 7, 'en', 0, 0, 7, 'higher node')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, db := newAltTextReader(t, Schema{HasNodeImage: true})
			if _, err := db.Exec(`
				INSERT INTO node_field_data VALUES (20, 7, 'en', 1), (10, 7, 'en', 1);
				INSERT INTO node__field_image VALUES ` + tc.values); err != nil {
				t.Fatal(err)
			}

			alts, err := reader.fileAltText(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if alts[7] != "lower node" {
				t.Fatalf("file 7 alt = %q, want lowest node owner", alts[7])
			}
			warnings := reader.Warnings()
			if len(warnings) != 1 || warnings[0] !=
				`Drupal file 7 selected node entity 10 alt "lower node"; discarded node entity 20 alt "higher node"` {
				t.Fatalf("warnings = %v, want deterministic selected/discarded provenance", warnings)
			}
		})
	}
}

func TestFileAltTextMediaSelectionIsIndependentOfRowOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values string
	}{
		{"higher owner inserted first", `(20, 9, 'en', 0, 0, 8, 'higher media'),
			(10, 9, 'en', 0, 0, 8, 'lower media')`},
		{"lower owner inserted first", `(10, 9, 'en', 0, 0, 8, 'lower media'),
			(20, 9, 'en', 0, 0, 8, 'higher media')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, db := newAltTextReader(t, Schema{HasMediaImg: true, HasMediaData: true})
			if _, err := db.Exec(`
				INSERT INTO media_field_data VALUES (20, 9, 'en', 1), (10, 9, 'en', 1);
				INSERT INTO media__field_media_image VALUES ` + tc.values); err != nil {
				t.Fatal(err)
			}

			alts, err := reader.fileAltText(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if alts[8] != "lower media" {
				t.Fatalf("file 8 alt = %q, want lowest media owner", alts[8])
			}
			warnings := reader.Warnings()
			if len(warnings) != 1 || warnings[0] !=
				`Drupal file 8 selected media entity 10 alt "lower media"; discarded media entity 20 alt "higher media"` {
				t.Fatalf("warnings = %v, want deterministic selected/discarded provenance", warnings)
			}
		})
	}
}

func TestResolveAltCandidatesPreservesMediaTierAndDeduplicates(t *testing.T) {
	candidates := []altCandidate{
		{FID: 9, OwnerID: 1, Alt: "node legacy", Source: altSourceNode},
		{FID: 9, OwnerID: 2, Alt: "media canonical", Source: altSourceNode},
		{FID: 9, OwnerID: 100, Alt: "media later", Source: altSourceMedia},
		{FID: 9, OwnerID: 99, Alt: "media canonical", Source: altSourceMedia},
		{FID: 9, OwnerID: 0, Alt: "", Source: altSourceMedia},
	}

	for _, input := range [][]altCandidate{
		append([]altCandidate(nil), candidates...),
		{candidates[4], candidates[3], candidates[2], candidates[1], candidates[0]},
	} {
		alts, conflicts := resolveAltCandidates(input)
		if alts[9] != "media canonical" {
			t.Fatalf("file 9 alt = %q, want media tier despite lower node owner", alts[9])
		}
		if len(conflicts) != 1 {
			t.Fatalf("conflicts = %+v, want one file conflict", conflicts)
		}
		conflict := conflicts[0]
		if conflict.Selected.Source != altSourceMedia || conflict.Selected.OwnerID != 99 {
			t.Fatalf("selected = %+v, want media owner 99", conflict.Selected)
		}
		if len(conflict.Discarded) != 2 || conflict.Discarded[0].Alt != "media later" ||
			conflict.Discarded[1].Alt != "node legacy" {
			t.Fatalf("discarded = %+v, want distinct media then node alternatives", conflict.Discarded)
		}
	}
}

func TestResolveAltCandidatesIgnoresEmptyAndIdenticalValues(t *testing.T) {
	alts, conflicts := resolveAltCandidates([]altCandidate{
		{FID: 10, OwnerID: 1, Alt: "", Source: altSourceMedia},
		{FID: 10, OwnerID: 2, Alt: "same", Source: altSourceNode},
		{FID: 10, OwnerID: 3, Alt: "same", Source: altSourceNode},
	})
	if alts[10] != "same" {
		t.Fatalf("file 10 alt = %q, want non-empty value", alts[10])
	}
	if len(conflicts) != 0 {
		t.Fatalf("identical values produced conflicts: %+v", conflicts)
	}
}

func TestFileAltTextFiltersNonSourceStaleDeletedAndNonZeroDeltaRows(t *testing.T) {
	reader, db := newAltTextReader(t, Schema{
		HasNodeImage: true, HasMediaImg: true, HasMediaData: true,
	})
	if _, err := db.Exec(`
		INSERT INTO node_field_data VALUES (1, 9, 'en', 1), (20, 7, 'en', 1);
		INSERT INTO node__field_image VALUES
			(1, 8, 'en', 0, 0, 11, 'stale node'),
			(1, 9, 'fr', 0, 0, 11, 'translated node'),
			(1, 9, 'en', 1, 0, 11, 'deleted node'),
			(1, 9, 'en', 0, 1, 11, 'delta node'),
			(20, 7, 'en', 0, 0, 11, 'current node');
		INSERT INTO media_field_data VALUES (1, 12, 'en', 1), (20, 13, 'en', 1);
		INSERT INTO media__field_media_image VALUES
			(1, 11, 'en', 0, 0, 12, 'stale media'),
			(1, 12, 'fr', 0, 0, 12, 'translated media'),
			(1, 12, 'en', 1, 0, 12, 'deleted media'),
			(1, 12, 'en', 0, 1, 12, 'delta media'),
			(20, 13, 'en', 0, 0, 12, 'current media');
	`); err != nil {
		t.Fatal(err)
	}

	alts, err := reader.fileAltText(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if alts[11] != "current node" || alts[12] != "current media" {
		t.Fatalf("alts = %v, want only source-language current active delta-zero rows", alts)
	}
	if warnings := reader.Warnings(); len(warnings) != 0 {
		t.Fatalf("filtered rows were reported as alternatives: %v", warnings)
	}
}

func TestAltConflictWarningsAreDeterministicAndBounded(t *testing.T) {
	conflicts := make([]altConflict, 0, maxAltConflictWarnings+2)
	longAlt := strings.Repeat("x", maxAltWarningRunes+20)
	for fid := maxAltConflictWarnings + 2; fid >= 1; fid-- {
		discarded := []altCandidate{{
			FID: int64(fid), OwnerID: 100, Alt: fmt.Sprintf("alternative-%02d", fid), Source: altSourceNode,
		}}
		if fid == 1 {
			discarded = discarded[:0]
			for owner := maxAltAlternativesPerWarning + 2; owner >= 1; owner-- {
				discarded = append(discarded, altCandidate{
					FID: 1, OwnerID: int64(owner), Alt: fmt.Sprintf("alternative-%02d", owner),
					Source: altSourceNode,
				})
			}
		}
		conflicts = append(conflicts, altConflict{
			FID: int64(fid),
			Selected: altCandidate{
				FID: int64(fid), OwnerID: 1, Alt: longAlt, Source: altSourceMedia,
			},
			Discarded: discarded,
		})
	}

	reader := &Reader{}
	reader.reportAltConflicts(conflicts)
	warnings := reader.Warnings()
	if len(warnings) != maxAltConflictWarnings+1 {
		t.Fatalf("warnings = %d, want %d bounded details plus aggregate: %v",
			len(warnings), maxAltConflictWarnings+1, warnings)
	}
	if !strings.Contains(warnings[0], "Drupal file 1 selected media entity 1") ||
		!strings.Contains(warnings[0], `node entity 1 alt "alternative-01"`) ||
		strings.Contains(warnings[0], `node entity 6 alt "alternative-06"`) ||
		strings.Contains(warnings[0], longAlt) || !strings.Contains(warnings[0], "…") {
		t.Fatalf("first warning is not deterministic and bounded: %q", warnings[0])
	}
	if !strings.Contains(warnings[maxAltConflictWarnings-1], "Drupal file 20 ") {
		t.Fatalf("last detailed warning = %q, want file 20", warnings[maxAltConflictWarnings-1])
	}
	if got := warnings[len(warnings)-1]; !strings.Contains(got,
		"omitted 2 file detail(s) and 4 discarded distinct alternative detail(s)") {
		t.Fatalf("aggregate warning = %q, want exact omission counts", got)
	}
}

// TestMatchTaxonomyRefField covers the field discovery that replaced a
// hardcoded node__field_tags.
//
// Two bug states, in order of severity.
//
// Discovering every node__field_*_target_id column and relying on a join to
// taxonomy_term_field_data to discard non-term references does not work: Drupal
// allocates file, media, user and term IDs from independent sequences, so a
// node referencing file 7 was assigned term 7 whenever term 7 existed. On a
// site with an image field and any vocabulary that silently mis-tags most
// pages. The field's configured target type is the only thing that separates
// them, so taxonomyFields is required here rather than advisory.
//
// Reading only node__field_tags, the version before that, meant a vocabulary
// referenced through field_category imported its terms and then had no page
// associations at all.
func TestMatchTaxonomyRefField(t *testing.T) {
	// As read from field.storage.node.* config.
	taxonomyFields := map[string]bool{
		"field_tags":       true,
		"field_category":   true,
		"field_topic_area": true,
	}

	for _, tc := range []struct {
		name       string
		table      string
		column     string
		wantPrefix string
		want       bool
	}{
		{"stock tags field", "node__field_tags", "field_tags_target_id", "node__field_", true},
		{"category field", "node__field_category", "field_category_target_id", "node__field_", true},
		{"multi-word field", "node__field_topic_area", "field_topic_area_target_id", "node__field_", true},
		{"uppercase table name", "NODE__FIELD_TAGS", "field_tags_target_id", "node__field_", true},
		{"prefixed install", "d9_node__field_tags", "field_tags_target_id", "d9_node__field_", true},

		// The one that corrupted data: shape-identical, but its target IDs are
		// file IDs from a different sequence.
		{"image field targets files, not terms", "node__field_image", "field_image_target_id", "node__field_", false},
		{"user reference field", "node__field_author", "field_author_target_id", "node__field_", false},

		{"other entity type", "media__field_tags", "field_tags_target_id", "node__field_", false},
		{"not a field table", "taxonomy_index", "tid_target_id", "node__field_", false},
		{"wrong install prefix", "node__field_tags", "field_tags_target_id", "d9_node__field_", false},
		{"unsafe identifier", "node__field_tags; DROP TABLE x", "field_tags_target_id", "node__field_", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := matchTaxonomyRefField(tc.table, tc.column, tc.wantPrefix, taxonomyFields)
			if ok != tc.want {
				t.Fatalf("matchTaxonomyRefField(%q, %q) ok = %v, want %v",
					tc.table, tc.column, ok, tc.want)
			}
			if ok && (got.Table == "" || got.Column == "") {
				t.Errorf("matched field is incomplete: %+v", got)
			}
		})
	}
}

// TestFieldNameFromStorageConfig pins the config-name parsing that decides which
// fields target taxonomy terms.
func TestFieldNameFromStorageConfig(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"field.storage.node.field_tags", "field_tags"},
		{"field.storage.node.field_category", "field_category"},
		{"field.storage.media.field_tags", ""},
		{"field.field.node.article.field_tags", ""},
		{"core.extension", ""},
		{"", ""},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := fieldNameFromStorageConfig(tc.in); got != tc.want {
				t.Errorf("fieldNameFromStorageConfig(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestTaxonomyTargetMarkerMatchesDrupalSerialization pins the marker against
// real PHP-serialized field storage config.
//
// The lengths in the marker are part of the format: "target_type" is 11 bytes
// and "taxonomy_term" is 13. A marker that does not match means every field
// looks non-taxonomy and page associations vanish, which is the failure that is
// easy to ship unnoticed because it produces no error.
func TestTaxonomyTargetMarkerMatchesDrupalSerialization(t *testing.T) {
	taxonomyStorage := `a:2:{s:8:"settings";a:1:{s:11:"target_type";s:13:"taxonomy_term";}s:4:"type";s:16:"entity_reference";}`
	fileStorage := `a:2:{s:8:"settings";a:1:{s:11:"target_type";s:4:"file";}s:4:"type";s:5:"image";}`
	userStorage := `a:2:{s:8:"settings";a:1:{s:11:"target_type";s:4:"user";}s:4:"type";s:16:"entity_reference";}`

	if !strings.Contains(taxonomyStorage, taxonomyTargetMarker) {
		t.Errorf("marker %q does not match a taxonomy_term field storage config", taxonomyTargetMarker)
	}
	for name, data := range map[string]string{"file": fileStorage, "user": userStorage} {
		if strings.Contains(data, taxonomyTargetMarker) {
			t.Errorf("marker matched a %s field storage config; it must not", name)
		}
	}
}

// TestPHPSerializedString covers the extractor that reads Drupal's config blobs.
//
// The lengths are part of the format, so an off-by-one here silently returns ""
// and every discovery falls back — a failure that produces no error at all.
func TestPHPSerializedString(t *testing.T) {
	imageType := `a:2:{s:6:"source";s:5:"image";s:20:"source_configuration";a:1:{s:12:"source_field";s:17:"field_media_image";}}`
	customType := `a:2:{s:6:"source";s:5:"image";s:20:"source_configuration";a:1:{s:12:"source_field";s:11:"field_photo";}}`

	for _, tc := range []struct {
		name, data, key, want string
	}{
		{"core image type", imageType, "source_field", "field_media_image"},
		{"custom source field", customType, "source_field", "field_photo"},
		{"absent key", imageType, "target_type", ""},
		{"empty data", "", "source_field", ""},
		{"truncated value", `s:12:"source_field";s:11:"field`, "source_field", ""},
		{"malformed length", `s:12:"source_field";s:xx:"field_photo";`, "source_field", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := phpSerializedString(tc.data, tc.key); got != tc.want {
				t.Errorf("phpSerializedString(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// TestIsSafeAliasPath covers which Drupal path aliases survive the import.
//
// util.IsValidAlias requires every segment to be a lowercase oCMS slug, which
// discarded aliases Drupal produces routinely — "About_Us", "News/Archive",
// anything non-ASCII — even though page_aliases stores arbitrary text and the
// frontend matches it exactly. Applying the admin form's grammar to imported
// data turned established URLs into 404s for no benefit.
func TestIsSafeAliasPath(t *testing.T) {
	for _, tc := range []struct {
		alias string
		want  bool
	}{
		// Ordinary Drupal aliases the slug grammar used to reject.
		{"about-us", true},
		{"About_Us", true},
		{"News/Archive", true},
		{"blog/2024/my-post", true},
		{"о-компании", true},
		{"produkte/grün", true},
		{"a.b.c", true},

		{"", false},
		{"/leading", false},
		{"trailing/", false},
		{"double//slash", false},
		{"has space", false},
		{"tab\there", false},
		{"query?x=1", false},
		{"frag#ment", false},
		{"back\\slash", false},
		{"http://evil.example/x", false},
		{"../escape", false},
		{"a/../b", false},
		{"a/./b", false},
		{"ctrl\x01char", false},
		{strings.Repeat("x", maxAliasLength+1), false},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			if got := isSafeAliasPath(tc.alias); got != tc.want {
				t.Errorf("isSafeAliasPath(%q) = %v, want %v", tc.alias, got, tc.want)
			}
		})
	}
}
