// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestListAndCount(t *testing.T) {
	t.Run("both succeed", func(t *testing.T) {
		items := []string{"a", "b", "c"}
		listFn := func() ([]string, error) { return items, nil }
		countFn := func() (int64, error) { return 3, nil }

		result, count, err := ListAndCount(listFn, countFn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 3 {
			t.Errorf("result length = %d, want 3", len(result))
		}
		if count != 3 {
			t.Errorf("count = %d, want 3", count)
		}
	})

	t.Run("list error", func(t *testing.T) {
		listFn := func() ([]int, error) { return nil, errors.New("list failed") }
		countFn := func() (int64, error) { return 10, nil }

		_, _, err := ListAndCount(listFn, countFn)
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("count error", func(t *testing.T) {
		listFn := func() ([]int, error) { return []int{1, 2, 3}, nil }
		countFn := func() (int64, error) { return 0, errors.New("count failed") }

		items, _, err := ListAndCount(listFn, countFn)
		if err == nil {
			t.Error("expected error")
		}
		// Items should still be returned even if count fails
		if len(items) != 3 {
			t.Errorf("items should be returned, got %d items", len(items))
		}
	})

	t.Run("empty list", func(t *testing.T) {
		listFn := func() ([]string, error) { return []string{}, nil }
		countFn := func() (int64, error) { return 0, nil }

		result, count, err := ListAndCount(listFn, countFn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("result length = %d, want 0", len(result))
		}
		if count != 0 {
			t.Errorf("count = %d, want 0", count)
		}
	})

	t.Run("with struct type", func(t *testing.T) {
		type Item struct {
			ID   int64
			Name string
		}
		items := []Item{{1, "First"}, {2, "Second"}}
		listFn := func() ([]Item, error) { return items, nil }
		countFn := func() (int64, error) { return 2, nil }

		result, count, err := ListAndCount(listFn, countFn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 2 {
			t.Errorf("result length = %d, want 2", len(result))
		}
		if count != 2 {
			t.Errorf("count = %d, want 2", count)
		}
		if result[0].Name != "First" {
			t.Errorf("first item name = %q, want %q", result[0].Name, "First")
		}
	})
}

func TestSaveBatchAssociations(t *testing.T) {
	t.Run("valid IDs", func(t *testing.T) {
		var savedIDs []int64
		saveFn := func(id int64) error {
			savedIDs = append(savedIDs, id)
			return nil
		}

		idStrs := []string{"1", "2", "3"}
		saveBatchAssociations(idStrs, saveFn, "test")

		if len(savedIDs) != 3 {
			t.Errorf("saved %d IDs, want 3", len(savedIDs))
		}
		for i, expected := range []int64{1, 2, 3} {
			if savedIDs[i] != expected {
				t.Errorf("savedIDs[%d] = %d, want %d", i, savedIDs[i], expected)
			}
		}
	})

	t.Run("invalid IDs skipped", func(t *testing.T) {
		var savedIDs []int64
		saveFn := func(id int64) error {
			savedIDs = append(savedIDs, id)
			return nil
		}

		idStrs := []string{"1", "invalid", "3", "abc", "5"}
		saveBatchAssociations(idStrs, saveFn, "test")

		if len(savedIDs) != 3 {
			t.Errorf("saved %d IDs, want 3", len(savedIDs))
		}
		expected := []int64{1, 3, 5}
		for i, exp := range expected {
			if savedIDs[i] != exp {
				t.Errorf("savedIDs[%d] = %d, want %d", i, savedIDs[i], exp)
			}
		}
	})

	t.Run("empty list", func(t *testing.T) {
		callCount := 0
		saveFn := func(id int64) error {
			callCount++
			return nil
		}

		saveBatchAssociations([]string{}, saveFn, "test")

		if callCount != 0 {
			t.Errorf("save function called %d times, want 0", callCount)
		}
	})

	t.Run("save error logged but continues", func(t *testing.T) {
		var savedIDs []int64
		saveFn := func(id int64) error {
			if id == 2 {
				return errors.New("save failed")
			}
			savedIDs = append(savedIDs, id)
			return nil
		}

		idStrs := []string{"1", "2", "3"}
		saveBatchAssociations(idStrs, saveFn, "test")

		// Should still process ID 3 even after ID 2 fails
		if len(savedIDs) != 2 {
			t.Errorf("saved %d IDs, want 2", len(savedIDs))
		}
	})
}

func TestBatchFetchRelated(t *testing.T) {
	type Parent struct {
		ID   int64
		Name string
	}

	type Child struct {
		ParentID int64
		Value    string
	}

	t.Run("fetch all successful", func(t *testing.T) {
		parents := []Parent{{1, "First"}, {2, "Second"}, {3, "Third"}}
		getID := func(p Parent) int64 { return p.ID }
		fetchFn := func(ctx context.Context, id int64) (Child, error) {
			return Child{ParentID: id, Value: "child"}, nil
		}

		result := batchFetchRelated(context.Background(), parents, getID, fetchFn, "test")

		if len(result) != 3 {
			t.Errorf("result length = %d, want 3", len(result))
		}
		for _, p := range parents {
			child, ok := result[p.ID]
			if !ok {
				t.Errorf("missing child for parent %d", p.ID)
				continue
			}
			if child.ParentID != p.ID {
				t.Errorf("child.ParentID = %d, want %d", child.ParentID, p.ID)
			}
		}
	})

	t.Run("some fetches fail", func(t *testing.T) {
		parents := []Parent{{1, "First"}, {2, "Second"}, {3, "Third"}}
		getID := func(p Parent) int64 { return p.ID }
		fetchFn := func(ctx context.Context, id int64) (Child, error) {
			if id == 2 {
				return Child{}, errors.New("fetch failed")
			}
			return Child{ParentID: id, Value: "child"}, nil
		}

		result := batchFetchRelated(context.Background(), parents, getID, fetchFn, "test")

		if len(result) != 2 {
			t.Errorf("result length = %d, want 2", len(result))
		}
		if _, ok := result[2]; ok {
			t.Error("result should not contain failed fetch")
		}
	})

	t.Run("not found is silent", func(t *testing.T) {
		parents := []Parent{{1, "First"}}
		getID := func(p Parent) int64 { return p.ID }
		fetchFn := func(ctx context.Context, id int64) (Child, error) {
			return Child{}, sql.ErrNoRows
		}

		result := batchFetchRelated(context.Background(), parents, getID, fetchFn, "test")

		if len(result) != 0 {
			t.Errorf("result length = %d, want 0", len(result))
		}
	})

	t.Run("empty parents list", func(t *testing.T) {
		var parents []Parent
		getID := func(p Parent) int64 { return p.ID }
		fetchFn := func(ctx context.Context, id int64) (Child, error) {
			t.Error("fetch should not be called")
			return Child{}, nil
		}

		result := batchFetchRelated(context.Background(), parents, getID, fetchFn, "test")

		if len(result) != 0 {
			t.Errorf("result length = %d, want 0", len(result))
		}
	})
}

func TestBatchFetchOptional(t *testing.T) {
	type Parent struct {
		ID      int64
		ChildID sql.NullInt64
	}

	type Child struct {
		ID    int64
		Value string
	}

	t.Run("fetch with valid optional IDs", func(t *testing.T) {
		parents := []Parent{
			{1, sql.NullInt64{Int64: 10, Valid: true}},
			{2, sql.NullInt64{Int64: 20, Valid: true}},
		}
		getID := func(p Parent) int64 { return p.ID }
		getOptionalID := func(p Parent) sql.NullInt64 { return p.ChildID }
		fetchFn := func(ctx context.Context, id int64) (Child, error) {
			return Child{ID: id, Value: "child"}, nil
		}

		result := batchFetchOptional(context.Background(), parents, getID, getOptionalID, fetchFn, "test")

		if len(result) != 2 {
			t.Errorf("result length = %d, want 2", len(result))
		}
		if result[1] == nil || result[1].ID != 10 {
			t.Errorf("result[1] = %+v, want &{ID:10}", result[1])
		}
	})

	t.Run("skip null optional IDs", func(t *testing.T) {
		parents := []Parent{
			{1, sql.NullInt64{Int64: 10, Valid: true}},
			{2, sql.NullInt64{Valid: false}}, // NULL
			{3, sql.NullInt64{Int64: 30, Valid: true}},
		}
		getID := func(p Parent) int64 { return p.ID }
		getOptionalID := func(p Parent) sql.NullInt64 { return p.ChildID }
		fetchFn := func(ctx context.Context, id int64) (Child, error) {
			return Child{ID: id, Value: "child"}, nil
		}

		result := batchFetchOptional(context.Background(), parents, getID, getOptionalID, fetchFn, "test")

		if len(result) != 2 {
			t.Errorf("result length = %d, want 2", len(result))
		}
		if _, ok := result[2]; ok {
			t.Error("result should not contain entry for NULL optional ID")
		}
	})

	t.Run("fetch error skipped", func(t *testing.T) {
		parents := []Parent{
			{1, sql.NullInt64{Int64: 10, Valid: true}},
			{2, sql.NullInt64{Int64: 20, Valid: true}},
		}
		getID := func(p Parent) int64 { return p.ID }
		getOptionalID := func(p Parent) sql.NullInt64 { return p.ChildID }
		fetchFn := func(ctx context.Context, id int64) (Child, error) {
			if id == 20 {
				return Child{}, errors.New("fetch failed")
			}
			return Child{ID: id, Value: "child"}, nil
		}

		result := batchFetchOptional(context.Background(), parents, getID, getOptionalID, fetchFn, "test")

		if len(result) != 1 {
			t.Errorf("result length = %d, want 1", len(result))
		}
	})
}

func TestTranslationBaseInfoStruct(t *testing.T) {
	// Test the struct initialization
	info := translationBaseInfo{
		TranslatedIDs: make(map[int64]bool),
	}

	if info.TranslatedIDs == nil {
		t.Error("TranslatedIDs should be initialized")
	}
	if info.EntityLanguage != nil {
		t.Error("EntityLanguage should be nil by default")
	}
	if len(info.AllLanguages) != 0 {
		t.Error("AllLanguages should be empty by default")
	}
}
