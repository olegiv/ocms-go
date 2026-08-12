// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build drupal_integration

// Package drupal integration tests run against a real Drupal 8-11 database.
// They are behind a build tag and additionally skip unless DRUPAL_HOST is set,
// mirroring the integration/fullimport tags on the Elefant source.
//
//	OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! \
//	DRUPAL_HOST=127.0.0.1 DRUPAL_USER=drupal DRUPAL_PASSWORD=drupal DRUPAL_DB=drupal \
//	DRUPAL_FILES=/var/www/html/sites/default/files \
//	go test -tags drupal_integration -v ./modules/migrator/sources/drupal/
package drupal

import (
	"context"
	"os"
	"testing"
	"time"
)

// liveConfig builds a source config from the environment, skipping when unset.
func liveConfig(t *testing.T) map[string]string {
	t.Helper()
	if os.Getenv("DRUPAL_HOST") == "" {
		t.Skip("DRUPAL_HOST not set; skipping live Drupal integration test")
	}
	return map[string]string{
		"mysql_host":     os.Getenv("DRUPAL_HOST"),
		"mysql_port":     EnvOrDefaultPort(),
		"mysql_user":     os.Getenv("DRUPAL_USER"),
		"mysql_password": os.Getenv("DRUPAL_PASSWORD"),
		"mysql_database": os.Getenv("DRUPAL_DB"),
		"table_prefix":   os.Getenv("DRUPAL_PREFIX"),
		"files_path":     os.Getenv("DRUPAL_FILES"),
	}
}

// EnvOrDefaultPort returns DRUPAL_PORT or the MySQL default.
func EnvOrDefaultPort() string {
	if p := os.Getenv("DRUPAL_PORT"); p != "" {
		return p
	}
	return "3306"
}

func TestLiveTestConnection(t *testing.T) {
	cfg := liveConfig(t)
	if err := NewSource().TestConnection(cfg); err != nil {
		t.Fatalf("TestConnection() error = %v", err)
	}
}

func TestLiveInspectReportsSchema(t *testing.T) {
	cfg := liveConfig(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	summary, err := NewSource().Inspect(ctx, cfg)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	t.Logf("nodes=%d translations=%d bundles=%v missing=%v",
		summary.Nodes, summary.Translations, summary.Bundles, summary.Missing)

	if len(summary.Bundles) == 0 {
		t.Error("Inspect() found no node bundles; is this an empty Drupal install?")
	}
}

func TestLiveReaderReadsCoreTables(t *testing.T) {
	cfg := liveConfig(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dsn, err := BuildDSN(cfg)
	if err != nil {
		t.Fatalf("BuildDSN() error = %v", err)
	}
	reader, err := NewReader(ctx, dsn, cfg["table_prefix"])
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	defer closeReader(reader)

	nodes, err := reader.GetNodes(ctx, 0)
	if err != nil {
		t.Fatalf("GetNodes() error = %v", err)
	}
	t.Logf("read %d nodes in the first batch", len(nodes))

	if _, err := reader.GetUsers(ctx); err != nil {
		t.Errorf("GetUsers() error = %v", err)
	}
	if _, err := reader.GetTerms(ctx); err != nil {
		t.Errorf("GetTerms() error = %v", err)
	}
	if _, err := reader.GetFiles(ctx); err != nil {
		t.Errorf("GetFiles() error = %v", err)
	}
	if _, err := reader.GetPathAliases(ctx); err != nil {
		t.Errorf("GetPathAliases() error = %v", err)
	}
	if _, err := reader.GetMenuLinks(ctx); err != nil {
		t.Errorf("GetMenuLinks() error = %v", err)
	}
	if _, _, err := reader.NodeImages(ctx); err != nil {
		t.Errorf("NodeImages() error = %v", err)
	}
	if _, err := reader.NodeTerms(ctx); err != nil {
		t.Errorf("NodeTerms() error = %v", err)
	}
}
