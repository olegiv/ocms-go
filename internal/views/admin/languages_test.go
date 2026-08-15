// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import "testing"

func TestLanguageCodeReadonly(t *testing.T) {
	tests := []struct {
		name string
		data LanguageFormData
		want bool
	}{
		{name: "create form remains editable"},
		{
			name: "ordinary existing code remains readonly",
			data: LanguageFormData{IsEdit: true, Language: &LanguageInfo{Code: "en"}},
			want: true,
		},
		{
			name: "legacy reserved code can be renamed",
			data: LanguageFormData{IsEdit: true, Language: &LanguageInfo{Code: "blog"}},
		},
		{
			name: "legacy invalid code can be renamed",
			data: LanguageFormData{IsEdit: true, Language: &LanguageInfo{Code: "x"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := languageCodeReadonly(tt.data); got != tt.want {
				t.Errorf("languageCodeReadonly() = %v, want %v", got, tt.want)
			}
		})
	}
}
