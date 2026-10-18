// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestAliasManagerXDataEncodesAliases checks that the alias list reaches
// Alpine as data. A form re-rendered after a validation error shows aliases
// as submitted, and the initializer used to quote them with '%s', so a quote
// in one ended the JavaScript string.
func TestAliasManagerXDataEncodesAliases(t *testing.T) {
	aliases := []PageFormAliasView{
		{ID: 7, Alias: "about/team"},
		{Alias: `x'),alert(1),('`},
		{Alias: `</script><b>"`},
	}
	got := aliasManagerXData(aliases)
	inner, ok := strings.CutPrefix(got, "aliasManager(")
	if !ok || !strings.HasSuffix(inner, ")") {
		t.Fatalf("aliasManagerXData() = %q, want aliasManager(<json>)", got)
	}
	var decoded []struct {
		Value string `json:"value"`
		ID    int64  `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSuffix(inner, ")")), &decoded); err != nil {
		t.Fatalf("aliasManagerXData() = %q is not JSON: %v", got, err)
	}
	var values []string
	for _, entry := range decoded {
		values = append(values, entry.Value)
	}
	want := []string{aliases[0].Alias, aliases[1].Alias, aliases[2].Alias}
	if !reflect.DeepEqual(values, want) || decoded[0].ID != 7 {
		t.Errorf("decoded %v, want values %v with the first ID 7", decoded, want)
	}
	if strings.Contains(got, "<") {
		t.Errorf("aliasManagerXData() = %q contains a raw '<'", got)
	}
	if got := aliasManagerXData(nil); got != "aliasManager([])" {
		t.Errorf("aliasManagerXData(nil) = %q, want aliasManager([])", got)
	}
}
