// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

var sourcePolicyTestID atomic.Uint64

func TestAPIKeySourceReplacementCancellationPreservesCallerTransaction(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	function := fmt.Sprintf("cancel_source_insert_%d", sourcePolicyTestID.Add(1))
	if err := sqlite.RegisterScalarFunction(function, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		cancel()
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	db, cleanup, parent, q := testSetup(t)
	defer cleanup()
	user := createTestUser(t, q, parent, "cancel-source@example.test")
	now := time.Now()
	key, err := q.CreateAPIKey(parent, CreateAPIKeyParams{Name: "source", KeyHash: "test-hash", KeyPrefix: "test", Permissions: `[]`, IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	old := []string{"198.51.100.4/32"}
	if err := q.ReplaceAPIKeySourceCIDRs(parent, key.ID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER cancel_source_insert BEFORE INSERT ON api_key_source_cidrs BEGIN SELECT ` + function + `(); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(parent, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := q.WithTx(tx).ReplaceAPIKeySourceCIDRs(ctx, key.ID, []string{"203.0.113.4/32"}); err == nil {
		t.Fatal("canceled replacement succeeded")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, err := q.ListAPIKeySourceCIDRs(parent, key.ID); err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("cancellation left a partial policy in caller transaction: %v, %v", got, err)
	}
}

func TestAPIKeySourceReplacementIsAtomicForReaders(t *testing.T) {
	paused, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	function := fmt.Sprintf("pause_source_insert_%d", sourcePolicyTestID.Add(1))
	if err := sqlite.RegisterScalarFunction(function, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		once.Do(func() { close(paused); <-resume })
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	user := createTestUser(t, q, ctx, "source@example.test")
	now := time.Now()
	key, err := q.CreateAPIKey(ctx, CreateAPIKeyParams{Name: "source", KeyHash: "test-hash", KeyPrefix: "test", Permissions: `[]`, IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	old := []string{"198.51.100.1/32"}
	if err := q.ReplaceAPIKeySourceCIDRs(ctx, key.ID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER pause_source_insert BEFORE INSERT ON api_key_source_cidrs BEGIN SELECT ` + function + `(); END`); err != nil {
		t.Fatal(err)
	}
	next := []string{"203.0.113.1/32", "2001:db8::1/128"}
	done := make(chan error, 1)
	go func() { done <- q.ReplaceAPIKeySourceCIDRs(ctx, key.ID, next) }()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement did not reach insert barrier")
	}
	if got, err := q.ListAPIKeySourceCIDRs(ctx, key.ID); err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("reader observed a partial/unrestricted policy: %v, %v", got, err)
	}
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, err := q.ListAPIKeySourceCIDRs(ctx, key.ID); err != nil || !reflect.DeepEqual(got, next) {
		t.Fatalf("committed replacement = %v, %v", got, err)
	}
	if err := q.ReplaceAPIKeySourceCIDRs(ctx, key.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := q.ListAPIKeySourceCIDRs(ctx, key.ID); err != nil || len(got) != 0 {
		t.Fatalf("intentional clear failed: %v, %v", got, err)
	}
}

func TestAPIKeySourceReplacementRollsBackFailuresAndRespectsCallerTransaction(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()
	user := createTestUser(t, q, ctx, "rollback@example.test")
	now := time.Now()
	key, err := q.CreateAPIKey(ctx, CreateAPIKeyParams{Name: "source", KeyHash: "test-hash", KeyPrefix: "test", Permissions: `[]`, IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	old := []string{"198.51.100.2/32"}
	if err := q.ReplaceAPIKeySourceCIDRs(ctx, key.ID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_source BEFORE INSERT ON api_key_source_cidrs WHEN NEW.cidr = 'reject' BEGIN SELECT RAISE(ABORT, 'test insertion failure'); END`); err != nil {
		t.Fatal(err)
	}
	for _, inCallerTransaction := range []bool{false, true} {
		t.Run(fmt.Sprintf("caller_transaction_%v", inCallerTransaction), func(t *testing.T) {
			writer := q
			if inCallerTransaction {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback() }()
				writer = q.WithTx(tx)
				if err := writer.ReplaceAPIKeySourceCIDRs(ctx, key.ID, []string{"203.0.113.2/32", "reject"}); err == nil {
					t.Fatal("insertion failure was ignored")
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			} else if err := writer.ReplaceAPIKeySourceCIDRs(ctx, key.ID, []string{"203.0.113.2/32", "reject"}); err == nil {
				t.Fatal("insertion failure was ignored")
			}
			if got, err := q.ListAPIKeySourceCIDRs(ctx, key.ID); err != nil || !reflect.DeepEqual(got, old) {
				t.Fatalf("failed replacement changed policy: %v, %v", got, err)
			}
		})
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := q.WithTx(tx).ReplaceAPIKeySourceCIDRs(ctx, key.ID, []string{"203.0.113.3/32"}); err != nil {
		t.Fatal(err)
	}
	if got, err := q.ListAPIKeySourceCIDRs(ctx, key.ID); err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("helper committed caller transaction: %v, %v", got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := q.ListAPIKeySourceCIDRs(ctx, key.ID); err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("caller rollback failed: %v, %v", got, err)
	}
}
