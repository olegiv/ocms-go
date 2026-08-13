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
