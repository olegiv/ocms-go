// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"strings"
	"testing"
)

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
