// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import "testing"

// TestMatchTaxonomyRefField covers the discovery that replaced a hardcoded
// node__field_tags.
//
// Bug state: reading only node__field_tags meant a vocabulary referenced
// through any other field — field_category is the common one — imported its
// terms and then had no page associations at all, so ticking "categories"
// produced a set of detached categories.
func TestMatchTaxonomyRefField(t *testing.T) {
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

		// Discovered on purpose: the name cannot tell a term reference from a
		// file one, so the taxonomy join in NodeTerms decides instead.
		{"image field is discovered, not name-filtered", "node__field_image", "field_image_target_id", "node__field_", true},

		{"other entity type", "media__field_tags", "field_tags_target_id", "node__field_", false},
		{"not a field table", "taxonomy_index", "tid_target_id", "node__field_", false},
		{"wrong install prefix", "node__field_tags", "field_tags_target_id", "d9_node__field_", false},
		{"unsafe identifier", "node__field_tags; DROP TABLE x", "field_tags_target_id", "node__field_", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := matchTaxonomyRefField(tc.table, tc.column, tc.wantPrefix)
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
