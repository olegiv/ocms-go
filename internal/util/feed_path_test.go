// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import "testing"

func TestIsFeedPath(t *testing.T) {
	for _, path := range []string{
		"rss.xml", "/atom.xml", "/ru/rss.xml", "/zh-hans/atom.xml",
		"/category/tech/rss.xml", "/tag/go/atom.xml", "/ru/category/tech/rss.xml", "/ru/tag/go/atom.xml",
	} {
		if !IsFeedPath(path) {
			t.Errorf("feed URL %q was not reserved", path)
		}
	}
	for _, path := range []string{
		"", "/news", "/rss", "/static/rss.xml", "/admin/rss.xml", "/rss.xml/extra",
		"/category/rss.xml", "/tag/invalid--slug/atom.xml", "/other/tech/rss.xml", "/ru/other/tech/rss.xml",
	} {
		if IsFeedPath(path) {
			t.Errorf("non-feed path %q was reserved", path)
		}
	}
}
