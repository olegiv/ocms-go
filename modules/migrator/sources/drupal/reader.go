// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	// Registers the "mysql" driver used by sql.Open below; the DSN itself is
	// built by shared.BuildMySQLDSN.
	_ "github.com/go-sql-driver/mysql"

	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
)

// Connection tuning for the source database. A migration is a long sequence of
// modest reads, so the pool stays small and every statement is bounded.
const (
	connectTimeout  = 10 * time.Second
	readTimeout     = 60 * time.Second
	maxOpenConns    = 4
	connMaxLifetime = 30 * time.Minute
	pingTimeout     = 10 * time.Second

	// batchSize bounds how many rows are pulled per query. Drupal sites can
	// carry tens of thousands of nodes; reading in batches keeps memory flat.
	batchSize = 500
)

// Drupal core table names, before the optional site prefix.
const (
	tableNodeData    = "node_field_data"
	tableNodeBody    = "node__body"
	tableNodeImage   = "node__field_image"
	tableNodeTags    = "node__field_tags"
	tableTermData    = "taxonomy_term_field_data"
	tableTermParent  = "taxonomy_term__parent"
	tableFileManaged = "file_managed"
	tableMedia       = "media"
	tableMediaImage  = "media__field_media_image"
	tableUserData    = "users_field_data"
	tablePathAlias   = "path_alias"
	// tableURLAlias is the pre-8.8 name. Drupal moved aliases to a content
	// entity in 8.8; on an older 8.x database only this table exists, and
	// treating that as "no aliases" regenerated every slug from its title and
	// dropped all legacy redirects.
	tableURLAlias     = "url_alias"
	tableMenuLinkData = "menu_link_content_data"
	// tableConfig holds Drupal's active configuration, which is the only place
	// a reference field's target entity type is recorded.
	tableConfig = "config"
	// tableMediaData carries media's translatable fields, including
	// default_langcode.
	tableMediaData = "media_field_data"
)

// Schema records which optional Drupal tables exist in the source database.
// Field tables are created per configured field, so a site without an image
// field simply has no node__field_image table — that is normal, not an error.
type Schema struct {
	HasNodeImage bool
	HasNodeBody  bool
	HasNodeTags  bool
	HasTermData  bool
	HasTermPar   bool
	HasFiles     bool
	HasMediaImg  bool
	HasMedia     bool
	HasAliases   bool
	// LegacyAliases is true when only the pre-8.8 url_alias table is present.
	LegacyAliases bool
	HasMenuLinks  bool
	HasConfig     bool
	HasMediaData  bool
}

// MissingOptional returns the optional tables that were not found, so the admin
// can be told what will be skipped instead of silently getting less content.
func (s Schema) MissingOptional() []string {
	var missing []string
	for _, check := range []struct {
		present bool
		name    string
	}{
		{s.HasNodeBody, tableNodeBody},
		{s.HasNodeImage, tableNodeImage},
		{s.HasNodeTags, tableNodeTags},
		{s.HasTermData, tableTermData},
		{s.HasTermPar, tableTermParent},
		{s.HasFiles, tableFileManaged},
		{s.HasMediaImg, tableMediaImage},
		{s.HasMedia, tableMedia},
		{s.HasAliases, tablePathAlias},
		{s.HasMenuLinks, tableMenuLinkData},
	} {
		if !check.present {
			missing = append(missing, check.name)
		}
	}
	return missing
}

// Reader reads content from a Drupal 11 MySQL database.
type Reader struct {
	db     *sql.DB
	prefix string
	schema Schema

	// present holds every table name in the source database, lowercased, so
	// per-bundle field tables can be probed without a round trip each.
	present    map[string]bool
	safePrefix string

	// warnings collects degraded-read notices the importer should surface. The
	// reader has no access to ImportResult, so without this an alt-text read
	// failure was visible only in the server log — a whole media library
	// imported with no alt attributes, reported as "0 errors, 0 notices".
	warnings []string
}

// Warnings returns and clears degraded-read notices accumulated so far.
func (r *Reader) Warnings() []string {
	w := r.warnings
	r.warnings = nil
	return w
}

// BuildDSN assembles a MySQL DSN from the submitted configuration.
//
// The assembly itself lives in shared.BuildMySQLDSN so that every source shares
// one hardened path: parameter-injection-safe escaping via mysql.Config, and a
// mandatory OCMS_MIGRATOR_ALLOWED_DB_HOSTS check.
func BuildDSN(cfg map[string]string) (string, error) {
	return shared.BuildMySQLDSN(cfg, shared.MySQLDSNOptions{
		ConnectTimeout: connectTimeout,
		ReadTimeout:    readTimeout,
	})
}

// NewReader opens a connection to the Drupal database and detects its schema.
func NewReader(ctx context.Context, dsn, tablePrefix string) (*Reader, error) {
	// Store the sanitizer's own output, never the raw config string, so the
	// tainted value cannot survive on the struct waiting for a future query
	// that forgets to re-sanitize.
	safePrefix, err := shared.SanitizeTablePrefix(tablePrefix)
	if err != nil {
		return nil, fmt.Errorf("invalid table prefix: %w", err)
	}

	db, openErr := sql.Open("mysql", dsn)
	if openErr != nil {
		return nil, fmt.Errorf("failed to open database: %w", openErr)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetConnMaxLifetime(connMaxLifetime)

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Error("failed to close drupal database after ping failure", "error", closeErr)
		}
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	r := &Reader{db: db, prefix: safePrefix}
	if err := r.detectSchema(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Error("failed to close drupal database after schema detection failure", "error", closeErr)
		}
		return nil, err
	}
	return r, nil
}

// Close closes the source database connection.
func (r *Reader) Close() error { return r.db.Close() }

// Schema returns the detected source schema.
func (r *Reader) Schema() Schema { return r.schema }

// table returns a backtick-quoted, prefixed table name safe for interpolation.
// The prefix is re-sanitized at every call site so the value used in SQL is
// always the sanitizer's own output, never the raw config string.
func (r *Reader) table(name string) (string, error) {
	safePrefix, err := shared.SanitizeTablePrefix(r.prefix)
	if err != nil {
		return "", fmt.Errorf("invalid table prefix: %w", err)
	}
	// The name is validated too, not just the prefix. Every caller passes a
	// package constant today, so this is not reachable — but "safe because all
	// 17 call sites happen to pass constants" is exactly the property that
	// stops holding when someone adds an eighteenth.
	safeName, err := shared.SanitizeIdentifier(name)
	if err != nil {
		return "", fmt.Errorf("invalid table name: %w", err)
	}
	return fmt.Sprintf("`%s%s`", safePrefix, safeName), nil
}

// detectSchema records which optional tables exist. Drupal's field tables vary
// per site, so the importer adapts instead of assuming a stock install.
func (r *Reader) detectSchema(ctx context.Context) error {
	safePrefix, err := shared.SanitizeTablePrefix(r.prefix)
	if err != nil {
		return fmt.Errorf("invalid table prefix: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT TABLE_NAME
		FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
	`)
	if err != nil {
		return fmt.Errorf("failed to read source schema: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	present := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("failed to scan table name: %w", err)
		}
		present[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating source schema: %w", err)
	}

	has := func(name string) bool {
		return present[strings.ToLower(safePrefix+name)]
	}

	if !has(tableNodeData) {
		return fmt.Errorf("table %s%s not found — is this a Drupal 8+ database?", safePrefix, tableNodeData)
	}

	r.present = present
	r.safePrefix = safePrefix
	r.schema = Schema{
		HasNodeBody:   has(tableNodeBody),
		HasNodeImage:  has(tableNodeImage),
		HasNodeTags:   has(tableNodeTags),
		HasTermData:   has(tableTermData),
		HasTermPar:    has(tableTermParent),
		HasFiles:      has(tableFileManaged),
		HasMediaImg:   has(tableMediaImage),
		HasMedia:      has(tableMedia),
		HasAliases:    has(tablePathAlias) || has(tableURLAlias),
		LegacyAliases: !has(tablePathAlias) && has(tableURLAlias),
		HasMenuLinks:  has(tableMenuLinkData),
		HasConfig:     has(tableConfig),
		HasMediaData:  has(tableMediaData),
	}
	return nil
}

// hasTable reports whether the source database has the prefixed table.
//
// This backs probes for tables that vary per media bundle. They are too
// numerous and too site-specific to each earn a Schema flag, which would spam
// every classic image-field site with irrelevant "optional table not found"
// notices.
func (r *Reader) hasTable(name string) bool {
	if r.present == nil {
		return false
	}
	return r.present[strings.ToLower(r.safePrefix+name)]
}

// NodeCount returns the number of default-language nodes.
func (r *Reader) NodeCount(ctx context.Context) (int, error) {
	tbl, err := r.table(tableNodeData)
	if err != nil {
		return 0, err
	}
	var count int
	// #nosec G201 -- tbl is rebuilt by Reader.table from a sanitized configured prefix and a constant table name.
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE default_langcode = 1", tbl)
	if err := r.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count nodes: %w", err)
	}
	return count, nil
}

// TranslationCount returns the number of non-default-language node rows. These
// are reported as skipped rather than imported.
func (r *Reader) TranslationCount(ctx context.Context) (int, error) {
	tbl, err := r.table(tableNodeData)
	if err != nil {
		return 0, err
	}
	var count int
	// #nosec G201 -- tbl is rebuilt by Reader.table from a sanitized configured prefix and a constant table name.
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE default_langcode <> 1", tbl)
	if err := r.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count node translations: %w", err)
	}
	return count, nil
}

// Bundles returns the distinct node bundles present, with their node counts, so
// the connection test can tell the admin what to map.
func (r *Reader) Bundles(ctx context.Context) (map[string]int, error) {
	tbl, err := r.table(tableNodeData)
	if err != nil {
		return nil, err
	}
	// #nosec G201 -- tbl is rebuilt by Reader.table from a sanitized configured prefix and a constant table name.
	query := fmt.Sprintf("SELECT type, COUNT(*) FROM %s WHERE default_langcode = 1 GROUP BY type ORDER BY type", tbl)
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list node bundles: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	bundles := make(map[string]int)
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, fmt.Errorf("failed to scan node bundle: %w", err)
		}
		bundles[name] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node bundles: %w", err)
	}
	return bundles, nil
}

// GetUsers returns registered users in the default language. uid 0 is Drupal's
// anonymous pseudo-user and is always excluded.
func (r *Reader) GetUsers(ctx context.Context) ([]User, error) {
	tbl, err := r.table(tableUserData)
	if err != nil {
		return nil, err
	}
	// #nosec G201 -- tbl is rebuilt by Reader.table from a sanitized configured prefix and a constant table name.
	query := fmt.Sprintf(`
		SELECT uid, COALESCE(name, ''), COALESCE(mail, ''), COALESCE(status, 0), COALESCE(created, 0)
		FROM %s
		WHERE uid > 0 AND default_langcode = 1 AND mail IS NOT NULL AND mail <> ''
		  AND status = 1
		ORDER BY uid`, tbl)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.UID, &u.Name, &u.Mail, &u.Status, &u.Created); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating users: %w", err)
	}
	return users, nil
}

// GetTerms returns taxonomy terms with their parent links resolved.
func (r *Reader) GetTerms(ctx context.Context) ([]Term, error) {
	if !r.schema.HasTermData {
		return nil, nil
	}
	tbl, err := r.table(tableTermData)
	if err != nil {
		return nil, err
	}

	query := buildTermQuery(tbl)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query taxonomy terms: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var terms []Term
	for rows.Next() {
		var t Term
		if err := rows.Scan(&t.TID, &t.Vocabulary, &t.Langcode, &t.Name, &t.Description, &t.Weight); err != nil {
			return nil, fmt.Errorf("failed to scan taxonomy term: %w", err)
		}
		terms = append(terms, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating taxonomy terms: %w", err)
	}

	parents, err := r.termParents(ctx)
	if err != nil {
		return nil, err
	}
	for i := range terms {
		terms[i].ParentTID = parents[terms[i].TID]
	}
	return terms, nil
}

func buildTermQuery(tbl string) string {
	return fmt.Sprintf(`
		SELECT tid, COALESCE(vid, ''), COALESCE(langcode, ''), COALESCE(name, ''),
		       description__value, COALESCE(weight, 0)
		FROM %s
		WHERE default_langcode = 1
		ORDER BY vid, weight, tid`, tbl)
}

// termParents maps term ID to parent term ID. Drupal writes a 0 parent row for
// root terms, which is preserved here as 0.
func (r *Reader) termParents(ctx context.Context) (map[int64]int64, error) {
	parents := make(map[int64]int64)
	if !r.schema.HasTermPar {
		return parents, nil
	}
	tbl, err := r.table(tableTermParent)
	if err != nil {
		return nil, err
	}

	termTbl, err := r.table(tableTermData)
	if err != nil {
		return nil, err
	}

	query := buildTermParentQuery(tbl, termTbl)
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query taxonomy hierarchy: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	chosen := make(map[int64]bool)
	for rows.Next() {
		var child, parent, delta int64
		if err := rows.Scan(&child, &parent, &delta); err != nil {
			return nil, fmt.Errorf("failed to scan taxonomy hierarchy: %w", err)
		}
		if delta != 0 {
			chosenParent := int64(0)
			if chosen[child] {
				chosenParent = parents[child]
			}
			r.warnings = append(r.warnings, fmt.Sprintf(
				"taxonomy term %d has an additional parent %d at delta %d; it was discarded in favor of delta-zero parent %d",
				child, parent, delta, chosenParent))
			continue
		}
		if !chosen[child] {
			chosen[child] = true
			if parent != 0 {
				parents[child] = parent
			}
			continue
		}
		r.warnings = append(r.warnings, fmt.Sprintf(
			"taxonomy term %d has an additional parent %d at delta %d; it was discarded in favor of parent %d",
			child, parent, delta, parents[child]))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating taxonomy hierarchy: %w", err)
	}
	return parents, nil
}

// buildTermParentQuery restricts parent references to the same source
// translation and current revision selected by GetTerms. Active non-zero
// deltas remain in the ordered result only so termParents can report discarded
// Drupal multi-parent data; they are never allowed to populate the parent map.
func buildTermParentQuery(parentTbl, termTbl string) string {
	return fmt.Sprintf(`
		SELECT p.entity_id, p.parent_target_id, p.delta
		FROM %s p
		JOIN %s t ON t.tid = p.entity_id
		 AND t.default_langcode = 1
		 AND p.langcode = t.langcode
		 AND p.revision_id = t.revision_id
		WHERE p.deleted = 0
		ORDER BY p.entity_id, p.delta`, parentTbl, termTbl)
}

// GetFiles returns permanent managed files, with alt text joined in from the
// image media field when that table exists.
func (r *Reader) GetFiles(ctx context.Context) ([]File, error) {
	if !r.schema.HasFiles {
		return nil, nil
	}
	tbl, err := r.table(tableFileManaged)
	if err != nil {
		return nil, err
	}

	// #nosec G201 -- tbl is rebuilt by Reader.table from a sanitized configured prefix and a constant table name.
	query := fmt.Sprintf(`
		SELECT fid, COALESCE(uuid, ''), COALESCE(filename, ''), COALESCE(uri, ''),
		       COALESCE(filemime, ''), COALESCE(filesize, 0), COALESCE(created, 0)
		FROM %s
		WHERE status = 1
		ORDER BY fid`, tbl)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query managed files: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var files []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.FID, &f.UUID, &f.Filename, &f.URI, &f.MimeType, &f.Size, &f.Created); err != nil {
			return nil, fmt.Errorf("failed to scan managed file: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating managed files: %w", err)
	}

	alts, err := r.fileAltText(ctx)
	if err != nil {
		// Alt text is a nice-to-have; a missing or malformed media field must
		// not cost the admin their entire media import. It is recorded as a
		// warning so the operator learns why every image arrived without one.
		slog.Warn("failed to read drupal media alt text", "error", err)
		r.warnings = append(r.warnings,
			fmt.Sprintf("alt text could not be read (%v); imported media has no alt attributes", err))
		return files, nil
	}
	for i := range files {
		if alt, ok := alts[files[i].FID]; ok {
			files[i].Alt = sql.NullString{String: alt, Valid: true}
		}
	}
	return files, nil
}

// buildAltQuery assembles a "(file id, owner entity id, alt text)" query over an
// image field table. Both shapes are identical apart from their column names, so
// they share one builder rather than two near-copies.
//
// Column names cannot be bound as parameters, so they are validated here rather
// than trusted from the caller. Every caller currently passes a literal, which
// is what kept this safe; validating makes that a property of the function
// instead of a property of the call sites.
func buildAltQuery(tbl, idColumn, altColumn string, entity entityJoin) (string, error) {
	safeID, err := shared.SanitizeIdentifier(idColumn)
	if err != nil {
		return "", err
	}
	safeAlt, err := shared.SanitizeIdentifier(altColumn)
	if err != nil {
		return "", err
	}

	// Without the join the field table is read across every language because
	// there is no owning data table against which to identify the source
	// translation or current revision. The total order still makes the degraded
	// fallback reproducible.
	if entity.Table == "" {
		return fmt.Sprintf(`
			SELECT f.%s, f.entity_id, COALESCE(f.%s, '')
			FROM %s f
			WHERE f.deleted = 0 AND f.delta = 0
			 AND f.%s IS NOT NULL
			ORDER BY f.%s, f.entity_id, f.revision_id DESC,
			         f.langcode, COALESCE(f.%s, '')`,
			safeID, safeAlt, tbl, safeID, safeID, safeAlt), nil
	}
	safeEntityID, err := shared.SanitizeIdentifier(entity.IDColumn)
	if err != nil {
		return "", err
	}
	safeRevision, err := shared.SanitizeIdentifier(entity.RevisionColumn)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`
			SELECT f.%s, f.entity_id, COALESCE(f.%s, '')
			FROM %s f
			JOIN %s e ON e.%s = f.entity_id
			 AND e.default_langcode = 1
			 AND f.langcode = e.langcode
			 AND f.revision_id = e.%s
			WHERE f.deleted = 0 AND f.delta = 0
			 AND f.%s IS NOT NULL
			ORDER BY f.%s, f.entity_id, f.revision_id DESC,
			         f.langcode, COALESCE(f.%s, '')`,
		safeID, safeAlt, tbl, entity.Table, safeEntityID, safeRevision, safeID,
		safeID, safeAlt), nil
}

// entityJoin names the entity data table a field table belongs to, so a field
// row can be restricted to the entity's default-language revision. A zero value
// means "no join available", which is the pre-8 shape and a site whose media
// data table is absent.
type entityJoin struct {
	Table          string // fully prefixed and sanitized
	IDColumn       string
	RevisionColumn string
}

// entityJoinFor builds the join describing a field table's owning entity.
//
// present is false when the site has no such table — a Drupal 8 install without
// the media module, for instance. The alt text is then read unfiltered, which
// is the old behaviour and only matters on a multilingual site.
func (r *Reader) entityJoinFor(table, idColumn string, present bool) (entityJoin, error) {
	if !present {
		return entityJoin{}, nil
	}
	tbl, err := r.table(table)
	if err != nil {
		return entityJoin{}, err
	}
	safeID, err := shared.SanitizeIdentifier(idColumn)
	if err != nil {
		return entityJoin{}, err
	}
	return entityJoin{Table: tbl, IDColumn: safeID, RevisionColumn: "vid"}, nil
}

type altSource string

const (
	altSourceNode  altSource = "node"
	altSourceMedia altSource = "media"

	// Alt conflict details are persisted with the import job. Bound both the
	// number and size of those messages so a heavily shared library cannot grow
	// the job row without limit.
	maxAltConflictWarnings       = 20
	maxAltAlternativesPerWarning = 5
	maxAltWarningRunes           = 120
)

// altCandidate preserves enough source provenance to make the unavoidable
// per-reference Drupal -> per-file oCMS collapse deterministic and auditable.
type altCandidate struct {
	FID     int64
	OwnerID int64
	Alt     string
	Source  altSource
}

type altConflict struct {
	FID       int64
	Selected  altCandidate
	Discarded []altCandidate
}

// readAltCandidates runs an alt-text query and returns every non-empty value
// together with its file and owning entity IDs. buildAltQuery supplies a total
// order; resolveAltCandidates applies the cross-table media-before-node policy.
func (r *Reader) readAltCandidates(ctx context.Context, table, idColumn, altColumn string,
	entity entityJoin, source altSource) ([]altCandidate, error) {

	var candidates []altCandidate
	tbl, err := r.table(table)
	if err != nil {
		return nil, err
	}

	query, err := buildAltQuery(tbl, idColumn, altColumn, entity)
	if err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query %s: %w", table, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var fid, ownerID int64
		var alt string
		if err := rows.Scan(&fid, &ownerID, &alt); err != nil {
			return nil, fmt.Errorf("failed to scan %s: %w", table, err)
		}
		if alt != "" {
			candidates = append(candidates, altCandidate{
				FID: fid, OwnerID: ownerID, Alt: alt, Source: source,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating %s: %w", table, err)
	}
	return candidates, nil
}

func altSourcePriority(source altSource) int {
	if source == altSourceMedia {
		return 0
	}
	return 1
}

func altCandidateLess(left, right altCandidate) bool {
	if left.FID != right.FID {
		return left.FID < right.FID
	}
	if left.Source != right.Source {
		return altSourcePriority(left.Source) < altSourcePriority(right.Source)
	}
	return left.OwnerID < right.OwnerID
}

// resolveAltCandidates chooses one global oCMS alt per Drupal file. Media
// entities are the canonical Drupal 8+ representation and therefore retain
// their existing precedence over classic node image fields. Within a tier the
// lowest owner entity ID wins; text is the deterministic final tie-breaker for
// malformed duplicate rows belonging to one owner.
func resolveAltCandidates(candidates []altCandidate) (map[int64]string, []altConflict) {
	nonEmpty := candidates[:0]
	for _, candidate := range candidates {
		if candidate.Alt != "" {
			nonEmpty = append(nonEmpty, candidate)
		}
	}
	candidates = nonEmpty

	// Stable sorting preserves buildAltQuery's revision/language/text order for
	// duplicate rows with the same tier and owner.
	sort.SliceStable(candidates, func(i, j int) bool {
		return altCandidateLess(candidates[i], candidates[j])
	})

	alts := make(map[int64]string)
	var conflicts []altConflict
	for start := 0; start < len(candidates); {
		end := start + 1
		for end < len(candidates) && candidates[end].FID == candidates[start].FID {
			end++
		}

		selected := candidates[start]
		alts[selected.FID] = selected.Alt
		seen := map[string]bool{selected.Alt: true}
		conflict := altConflict{FID: selected.FID, Selected: selected}
		for _, candidate := range candidates[start+1 : end] {
			if candidate.Alt == "" || seen[candidate.Alt] {
				continue
			}
			seen[candidate.Alt] = true
			conflict.Discarded = append(conflict.Discarded, candidate)
		}
		if len(conflict.Discarded) > 0 {
			conflicts = append(conflicts, conflict)
		}
		start = end
	}
	return alts, conflicts
}

func quoteAltForWarning(alt string) string {
	runes := []rune(alt)
	if len(runes) > maxAltWarningRunes {
		runes = append(runes[:maxAltWarningRunes-1], '…')
	}
	return strconv.Quote(string(runes))
}

func describeAltCandidate(candidate altCandidate) string {
	return fmt.Sprintf("%s entity %d alt %s", candidate.Source, candidate.OwnerID,
		quoteAltForWarning(candidate.Alt))
}

// reportAltConflicts names the deterministic winner and bounded discarded
// alternatives. The final aggregate makes every omitted detail visible even
// when the source contains more conflicts than are safe to persist individually.
func (r *Reader) reportAltConflicts(conflicts []altConflict) {
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].FID < conflicts[j].FID })

	omittedFiles := 0
	omittedAlternatives := 0
	for i, conflict := range conflicts {
		if i >= maxAltConflictWarnings {
			omittedFiles++
			omittedAlternatives += len(conflict.Discarded)
			continue
		}

		sort.SliceStable(conflict.Discarded, func(i, j int) bool {
			return altCandidateLess(conflict.Discarded[i], conflict.Discarded[j])
		})
		shown := min(len(conflict.Discarded), maxAltAlternativesPerWarning)
		discarded := make([]string, 0, shown)
		for _, candidate := range conflict.Discarded[:shown] {
			discarded = append(discarded, describeAltCandidate(candidate))
		}
		omittedAlternatives += len(conflict.Discarded) - shown
		r.warnings = append(r.warnings, fmt.Sprintf(
			"Drupal file %d selected %s; discarded %s",
			conflict.FID, describeAltCandidate(conflict.Selected), strings.Join(discarded, "; ")))
	}

	if omittedFiles > 0 || omittedAlternatives > 0 {
		r.warnings = append(r.warnings, fmt.Sprintf(
			"Drupal alt-text conflict report omitted %d file detail(s) and %d discarded distinct alternative detail(s) after the bounded limit",
			omittedFiles, omittedAlternatives))
	}
}

// coreMediaSourceFields are the source fields Drupal core's own media types
// declare. They are the fallback when media type configuration cannot be read;
// their target type is fixed by core, so using them is safe even unverified.
var coreMediaSourceFields = []string{
	"field_media_image",
	"field_media_document",
	"field_media_file",
	"field_media_audio_file",
	"field_media_video_file",
}

// mediaSourceFieldMarkerKey is the config key naming a media type's source
// field. Drupal stores media.type.<bundle> as PHP-serialized data.
const mediaSourceFieldMarkerKey = "source_field"

// mediaSourceFields returns the field names this site's media types use to hold
// their file, discovered from media.type.* configuration.
//
// A fixed allowlist of core field names missed any media type with a custom
// source field — field_photo, say. CKEditor embeds carry the MEDIA entity UUID,
// so a media entity whose source field was not read never entered the UUID map,
// and replaceDrupalMedia deleted every one of its embeds from the body even
// though the underlying file had been copied.
//
// The names come from configuration rather than from table shape for the same
// reason taxonomy fields do: a media__field_* table with a *_target_id column
// may point at terms or other media, and Drupal's entity IDs are independent
// sequences, so nothing about the shape distinguishes them.
func (r *Reader) mediaSourceFields(ctx context.Context) []string {
	if !r.schema.HasConfig {
		return coreMediaSourceFields
	}
	tbl, err := r.table(tableConfig)
	if err != nil {
		return coreMediaSourceFields
	}

	// #nosec G201 -- tbl is rebuilt by Reader.table from a sanitized configured prefix and a constant table name.
	query := fmt.Sprintf(`
		SELECT data FROM %s
		WHERE collection = '' AND name LIKE 'media.type.%%'`, tbl)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		slog.Warn("failed to read media type configuration", "error", err)
		r.warnings = append(r.warnings, fmt.Sprintf(
			"media type configuration could not be read (%v); only Drupal core's "+
				"media fields were used, so embeds of custom media types may be dropped", err))
		return coreMediaSourceFields
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	seen := make(map[string]bool, len(coreMediaSourceFields))
	fields := make([]string, 0, len(coreMediaSourceFields))
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		fields = append(fields, name)
	}
	// Core's fields stay in the list: a bundle can exist without its config row
	// being readable, and an absent table is skipped harmlessly below.
	for _, name := range coreMediaSourceFields {
		add(name)
	}

	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			slog.Warn("failed to scan media type configuration", "error", err)
			return fields
		}
		add(phpSerializedString(data, mediaSourceFieldMarkerKey))
	}
	if err := rows.Err(); err != nil {
		slog.Warn("error iterating media type configuration", "error", err)
	}
	sort.Strings(fields)
	return fields
}

// phpSerializedString extracts a string value from PHP-serialized data by key.
//
// Only the one shape Drupal writes is handled: s:<len>:"<key>";s:<len>:"<value>".
// An unparseable or absent key yields "", which callers treat as "not found"
// rather than guessing.
func phpSerializedString(data, key string) string {
	marker := fmt.Sprintf(`s:%d:"%s";s:`, len(key), key)
	idx := strings.Index(data, marker)
	if idx < 0 {
		return ""
	}
	rest := data[idx+len(marker):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	length, err := strconv.Atoi(rest[:colon])
	if err != nil || length < 0 {
		return ""
	}
	rest = rest[colon+1:]
	if len(rest) < length+2 || rest[0] != '"' {
		return ""
	}
	return rest[1 : length+1]
}

// MediaUUIDsByFile maps a file ID to the UUIDs of the media entities that
// reference it.
//
// Drupal 9/10/11's CKEditor 5 emits <drupal-media data-entity-uuid="…">
// carrying the MEDIA entity UUID, not the file UUID. Resolving those embeds
// against file UUIDs alone never matches, and an unresolved embed is deleted
// from the body — so every media-library image vanishes with no error and no
// notice. Reading this mapping is what makes those embeds resolvable.
//
// A site with no media module has no media table; this returns an empty map and
// the file-UUID path continues to work unchanged.
func (r *Reader) MediaUUIDsByFile(ctx context.Context) (map[int64][]string, error) {
	byFile := make(map[int64][]string)
	if !r.schema.HasMedia {
		return byFile, nil
	}
	if !r.schema.HasMediaData {
		return nil, fmt.Errorf("%s is absent; media entity UUID references cannot be mapped to their current source-language revision", tableMediaData)
	}
	mediaTbl, err := r.table(tableMedia)
	if err != nil {
		return nil, err
	}
	mediaDataTbl, err := r.table(tableMediaData)
	if err != nil {
		return nil, err
	}

	for _, field := range r.mediaSourceFields(ctx) {
		table := "media__" + field
		if !r.hasTable(table) {
			continue
		}
		fieldTbl, err := r.table(table)
		if err != nil {
			return nil, err
		}

		if err := r.collectMediaUUIDs(ctx, mediaTbl, mediaDataTbl, fieldTbl,
			field+"_target_id", table, byFile); err != nil {
			return nil, err
		}
	}
	return byFile, nil
}

// collectMediaUUIDs adds one media field table's file references to byFile.
func (r *Reader) collectMediaUUIDs(ctx context.Context, mediaTbl, mediaDataTbl, fieldTbl, column, label string,
	byFile map[int64][]string) error {
	query, err := buildMediaUUIDQuery(mediaTbl, mediaDataTbl, fieldTbl, column)
	if err != nil {
		return err
	}

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query %s: %w", label, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var mediaUUID string
		var fid int64
		if err := rows.Scan(&mediaUUID, &fid); err != nil {
			return fmt.Errorf("failed to scan %s: %w", label, err)
		}
		byFile[fid] = append(byFile[fid], mediaUUID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating %s: %w", label, err)
	}
	return nil
}

// buildMediaUUIDQuery selects only the active file reference for the media
// entity's current source-language revision. Drupal field tables keep rows for
// older revisions, translations, deleted values, and every delta.
func buildMediaUUIDQuery(mediaTbl, mediaDataTbl, fieldTbl, column string) (string, error) {

	// As in buildAltQuery: the column name is interpolated, so validate it here
	// rather than relying on every caller to pass a literal.
	safeColumn, err := shared.SanitizeIdentifier(column)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`
		SELECT m.uuid, f.%s
		FROM %s m
		JOIN %s d ON d.mid = m.mid AND d.vid = m.vid AND d.default_langcode = 1
		JOIN %s f ON f.entity_id = d.mid
		 AND f.revision_id = d.vid AND f.langcode = d.langcode
		WHERE f.deleted = 0 AND f.delta = 0
		 AND f.%s IS NOT NULL AND m.uuid <> ''`, safeColumn, mediaTbl, mediaDataTbl, fieldTbl, safeColumn), nil
}

// fileAltText maps file ID to alt text.
//
// Alt text lives in two places depending on how the site stores images.
// media__field_media_image is the Drupal 8+ media-entity shape; a classic
// image-field site has no media entities at all and keeps its alt on
// node__field_image, keyed by the same file ID. Reading only the media field
// silently drops every alt on a classic site, so both are read and merged with
// the media field winning — a site can carry a media library and legacy image
// fields at once.
//
// Drupal's alt is per field instance, so two owners may describe one shared file
// differently while oCMS stores one alt per media row. Media owners win over
// node owners, then the lowest owner entity ID wins. Distinct discarded values
// are reported with bounded source provenance so that collapse is auditable.
func (r *Reader) fileAltText(ctx context.Context) (map[int64]string, error) {
	var candidates []altCandidate

	if r.schema.HasNodeImage {
		nodeEntity, err := r.entityJoinFor(tableNodeData, "nid", true)
		if err != nil {
			return nil, err
		}
		nodeAlts, err := r.readAltCandidates(ctx, tableNodeImage,
			"field_image_target_id", "field_image_alt", nodeEntity, altSourceNode)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, nodeAlts...)
	}

	if r.schema.HasMediaImg {
		mediaEntity, err := r.entityJoinFor(tableMediaData, "mid", r.schema.HasMediaData)
		if err != nil {
			return nil, err
		}
		mediaAlts, err := r.readAltCandidates(ctx, tableMediaImage,
			"field_media_image_target_id", "field_media_image_alt", mediaEntity, altSourceMedia)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, mediaAlts...)
	}

	alts, conflicts := resolveAltCandidates(candidates)
	r.reportAltConflicts(conflicts)
	return alts, nil
}

// buildNodeQuery assembles the node batch query.
//
// node__body is a *field* table, not core: a site whose content types carry no
// body field simply does not have it, and joining it unconditionally makes the
// entire node import fail with "table doesn't exist". When it is absent the
// body columns are selected as NULL so the scan arity stays the same and nodes
// still import — titles, slugs, aliases and taxonomy links are worth having
// even without body text.
func buildNodeQuery(nodeTbl, bodyTbl string, hasBody bool) string {
	const baseColumns = `n.nid, n.type, COALESCE(n.langcode, ''), COALESCE(n.title, ''),
		       COALESCE(n.status, 0), COALESCE(n.uid, 0),
		       COALESCE(n.created, 0), COALESCE(n.changed, 0)`

	if !hasBody {
		return fmt.Sprintf(`
		SELECT %s,
		       NULL, NULL, NULL
		FROM %s n
		WHERE n.default_langcode = 1
		ORDER BY n.nid
		LIMIT ? OFFSET ?`, baseColumns, nodeTbl)
	}

	return fmt.Sprintf(`
		SELECT %s,
		       b.body_value, b.body_summary, b.body_format
		FROM %s n
		LEFT JOIN %s b ON b.entity_id = n.nid
		 AND b.langcode = n.langcode
		 AND b.revision_id = n.vid
		 AND b.deleted = 0
		 AND b.delta = 0
		WHERE n.default_langcode = 1
		ORDER BY n.nid
		LIMIT ? OFFSET ?`, baseColumns, nodeTbl, bodyTbl)
}

// GetNodes returns one batch of default-language nodes ordered by nid, with
// body text joined in when the site has a body field.
func (r *Reader) GetNodes(ctx context.Context, offset int) ([]Node, error) {
	nodeTbl, err := r.table(tableNodeData)
	if err != nil {
		return nil, err
	}
	bodyTbl, err := r.table(tableNodeBody)
	if err != nil {
		return nil, err
	}

	query := buildNodeQuery(nodeTbl, bodyTbl, r.schema.HasNodeBody)

	rows, err := r.db.QueryContext(ctx, query, batchSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var nodes []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.NID, &n.Type, &n.Langcode, &n.Title, &n.Status,
			&n.UID, &n.Created, &n.Changed, &n.Body, &n.Summary, &n.Format); err != nil {
			return nil, fmt.Errorf("failed to scan node: %w", err)
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating nodes: %w", err)
	}
	return nodes, nil
}

// GetNodeLanguages returns the source translation language for every node.
// Path aliases are loaded before the paginated node stage (taxonomy aliases
// must already yield to node URLs), so this lightweight map is needed to place
// language-neutral aliases in the owning node's concrete public namespace.
func (r *Reader) GetNodeLanguages(ctx context.Context) (map[int64]string, error) {
	nodeTbl, err := r.table(tableNodeData)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT nid, COALESCE(langcode, '')
		FROM %s
		WHERE default_langcode = 1
		ORDER BY nid`, nodeTbl))
	if err != nil {
		return nil, fmt.Errorf("failed to query node languages: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	languages := make(map[int64]string)
	for rows.Next() {
		var nid int64
		var language string
		if err := rows.Scan(&nid, &language); err != nil {
			return nil, fmt.Errorf("failed to scan node language: %w", err)
		}
		languages[nid] = language
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node languages: %w", err)
	}
	return languages, nil
}

// NodeImages maps node ID to its featured-image file ID.
//
// Alt text is deliberately not returned here. It belongs to the file, not the
// node, and reaches the media row through fileAltText; returning it from this
// function too produced a second copy that every caller ignored, which is how
// alt text came to be dropped silently.
func (r *Reader) NodeImages(ctx context.Context) (map[int64]int64, error) {
	images := make(map[int64]int64)
	if !r.schema.HasNodeImage {
		return images, nil
	}
	tbl, err := r.table(tableNodeImage)
	if err != nil {
		return nil, err
	}

	nodeTbl, err := r.table(tableNodeData)
	if err != nil {
		return nil, err
	}

	// Joined to the node row and matched on langcode. Field tables carry one
	// row per translation, so reading them unfiltered let whichever translation
	// happened to be scanned last become the featured image of a page imported
	// from the default-language row.
	query := buildNodeImageQuery(tbl, nodeTbl)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query node images: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var nid, fid int64
		if err := rows.Scan(&nid, &fid); err != nil {
			return nil, fmt.Errorf("failed to scan node image: %w", err)
		}
		images[nid] = fid
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node images: %w", err)
	}
	return images, nil
}

func buildNodeImageQuery(tbl, nodeTbl string) string {
	return fmt.Sprintf(`
		SELECT f.entity_id, f.field_image_target_id
		FROM %s f
		JOIN %s n ON n.nid = f.entity_id
		 AND n.default_langcode = 1
		 AND f.langcode = n.langcode
		 AND f.revision_id = n.vid
		WHERE f.deleted = 0 AND f.delta = 0 AND f.field_image_target_id IS NOT NULL`, tbl, nodeTbl)
}

// taxonomyRefField is one discovered entity-reference field table.
type taxonomyRefField struct {
	Table  string // fully prefixed and sanitized
	Column string // sanitized "field_<name>_target_id"
}

// taxonomyTargetMarker is how Drupal's PHP-serialized field storage config
// records that a reference field points at taxonomy terms. The "11" and "13"
// are the fixed lengths of "target_type" and "taxonomy_term".
const taxonomyTargetMarker = `s:11:"target_type";s:13:"taxonomy_term"`

// taxonomyFieldNames returns the node field names whose storage config declares
// taxonomy_term as its target entity type.
//
// This has to be read from configuration. The previous version discovered every
// node__field_*_target_id column and relied on a join to taxonomy_term_field_data
// to discard non-term references — which does not work: Drupal allocates file,
// media, user and term IDs from independent sequences, so a node referencing
// file 7 was assigned term 7 whenever term 7 existed. On any site with an image
// field and a vocabulary that silently corrupts the tags and categories of most
// pages, which is far worse than the missing associations it set out to fix.
func (r *Reader) taxonomyFieldNames(ctx context.Context) (map[string]bool, error) {
	if !r.schema.HasConfig {
		return nil, fmt.Errorf("table %s not found", tableConfig)
	}
	tbl, err := r.table(tableConfig)
	if err != nil {
		return nil, err
	}

	// Active config lives in the database by default. The collection filter
	// keeps language overrides out; they carry no target_type of their own.
	// #nosec G201 -- tbl is rebuilt by Reader.table from a sanitized configured prefix and a constant table name.
	query := fmt.Sprintf(`
		SELECT name, data
		FROM %s
		WHERE collection = '' AND name LIKE 'field.storage.node.%%'`, tbl)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to read field storage config: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	names := make(map[string]bool)
	for rows.Next() {
		var name, data string
		if err := rows.Scan(&name, &data); err != nil {
			return nil, fmt.Errorf("failed to scan field storage config: %w", err)
		}
		if !strings.Contains(data, taxonomyTargetMarker) {
			continue
		}
		if field := fieldNameFromStorageConfig(name); field != "" {
			names[field] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating field storage config: %w", err)
	}
	return names, nil
}

// fieldNameFromStorageConfig extracts "field_category" from
// "field.storage.node.field_category".
func fieldNameFromStorageConfig(configName string) string {
	const prefix = "field.storage.node."
	if !strings.HasPrefix(configName, prefix) {
		return ""
	}
	return strings.TrimPrefix(configName, prefix)
}

// taxonomyRefFields returns the field tables holding this site's node-to-term
// references.
//
// Table names are discovered rather than derived from the field name, because
// Drupal truncates and hashes names over 48 characters; the column name is what
// ties a table back to its field.
func (r *Reader) taxonomyRefFields(ctx context.Context) ([]taxonomyRefField, error) {
	taxonomyFields, err := r.taxonomyFieldNames(ctx)
	if err != nil {
		// Without the field configuration there is no way to tell a term
		// reference from a file one, so fall back to the single field whose
		// target type is fixed by Drupal core rather than guessing. Importing
		// fewer associations is recoverable; importing wrong ones is not.
		slog.Warn("failed to read taxonomy field configuration", "error", err)
		r.warnings = append(r.warnings, fmt.Sprintf(
			"taxonomy field configuration could not be read (%v); only %s was used, "+
				"so pages may be missing categories from other reference fields",
			err, tableNodeTags))
		taxonomyFields = map[string]bool{"field_tags": true}
	}
	if len(taxonomyFields) == 0 {
		return nil, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT TABLE_NAME, COLUMN_NAME
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND COLUMN_NAME LIKE '%_target_id'
		ORDER BY TABLE_NAME, COLUMN_NAME`)
	if err != nil {
		return nil, fmt.Errorf("failed to discover taxonomy reference fields: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	// Matching in Go rather than in the LIKE keeps the prefix out of a pattern
	// where "_" is a wildcard.
	wantPrefix := strings.ToLower(r.safePrefix + "node__field_")

	var fields []taxonomyRefField
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, fmt.Errorf("failed to scan reference field: %w", err)
		}
		if f, ok := matchTaxonomyRefField(table, column, wantPrefix, taxonomyFields); ok {
			fields = append(fields, f)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating reference fields: %w", err)
	}
	return fields, nil
}

// matchTaxonomyRefField decides whether one (table, column) pair is a node
// reference field that points at taxonomy terms.
//
// taxonomyFields is the authority. Matching on shape alone accepts
// node__field_image, whose field_image_target_id holds file IDs from a separate
// sequence, and no amount of joining can tell those apart from term IDs.
func matchTaxonomyRefField(table, column, wantPrefix string, taxonomyFields map[string]bool) (taxonomyRefField, bool) {
	if !strings.HasPrefix(strings.ToLower(table), wantPrefix) {
		return taxonomyRefField{}, false
	}
	field := strings.TrimSuffix(strings.ToLower(column), "_target_id")
	if field == strings.ToLower(column) || !taxonomyFields[field] {
		return taxonomyRefField{}, false
	}
	safeTable, err := shared.SanitizeIdentifier(table)
	if err != nil {
		return taxonomyRefField{}, false
	}
	safeColumn, err := shared.SanitizeIdentifier(column)
	if err != nil {
		return taxonomyRefField{}, false
	}
	return taxonomyRefField{Table: safeTable, Column: safeColumn}, true
}

// NodeTerms maps node ID to the taxonomy term IDs it references, across every
// entity-reference field the site defines.
func (r *Reader) NodeTerms(ctx context.Context) (map[int64][]int64, error) {
	terms := make(map[int64][]int64)
	// Without the taxonomy table there is nothing to join against, and no terms
	// were imported either.
	if !r.schema.HasTermData {
		return terms, nil
	}

	fields, err := r.taxonomyRefFields(ctx)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return terms, nil
	}

	nodeTbl, err := r.table(tableNodeData)
	if err != nil {
		return nil, err
	}
	termTbl, err := r.table(tableTermData)
	if err != nil {
		return nil, err
	}

	seen := make(map[int64]map[int64]bool)
	for _, f := range fields {
		// The taxonomy join only drops references to terms that no longer
		// exist; it is not what makes discovery safe. Field configuration is
		// — see taxonomyFieldNames. The node join filters to the imported
		// language, as NodeImages does: unfiltered, this appended the terms of
		// every translation onto the default-language page.
		query := buildNodeTermsQuery(f, nodeTbl, termTbl)

		if err := r.collectNodeTerms(ctx, query, terms, seen); err != nil {
			return nil, err
		}
	}
	return terms, nil
}

func buildNodeTermsQuery(field taxonomyRefField, nodeTbl, termTbl string) string {
	return fmt.Sprintf(`
		SELECT f.entity_id, f.%s
		FROM %s f
		JOIN %s n ON n.nid = f.entity_id
		 AND n.default_langcode = 1
		 AND f.langcode = n.langcode
		 AND f.revision_id = n.vid
		JOIN %s t ON t.tid = f.%s AND t.default_langcode = 1
		WHERE f.deleted = 0 AND f.%s IS NOT NULL
		ORDER BY f.entity_id, f.delta`,
		field.Column, field.Table, nodeTbl, termTbl, field.Column, field.Column)
}

// collectNodeTerms runs one field table's query and merges its rows, skipping
// a term already recorded for that node so two fields referencing the same term
// do not associate it twice.
func (r *Reader) collectNodeTerms(ctx context.Context, query string,
	terms map[int64][]int64, seen map[int64]map[int64]bool) error {

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query node taxonomy references: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var nid, tid int64
		if err := rows.Scan(&nid, &tid); err != nil {
			return fmt.Errorf("failed to scan node taxonomy reference: %w", err)
		}
		if seen[nid] == nil {
			seen[nid] = make(map[int64]bool)
		}
		if seen[nid][tid] {
			continue
		}
		seen[nid][tid] = true
		terms[nid] = append(terms[nid], tid)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating node taxonomy references: %w", err)
	}
	return nil
}

// aliasSourceColumn returns the column holding the system path. Drupal 8.8
// renamed url_alias.source to path_alias.path.
func aliasSourceColumn(schema Schema) string {
	if schema.LegacyAliases {
		return "source"
	}
	return "path"
}

// GetPathAliases returns published path aliases in the default language.
func (r *Reader) GetPathAliases(ctx context.Context) ([]PathAlias, error) {
	if !r.schema.HasAliases {
		return nil, nil
	}
	// The pre-8.8 url_alias table has no status column and names its key "pid"
	// rather than "id"; everything else lines up, so one query shape covers
	// both once those two differences are papered over.
	name, idCol, statusExpr, whereClause := tablePathAlias, "id", "COALESCE(status, 1)", "WHERE status = 1"
	if r.schema.LegacyAliases {
		name, idCol, statusExpr, whereClause = tableURLAlias, "pid", "1", ""
	}
	tbl, err := r.table(name)
	if err != nil {
		return nil, err
	}
	safeID, err := shared.SanitizeIdentifier(idCol)
	if err != nil {
		return nil, err
	}

	// #nosec G201 -- identifiers are sanitized by Reader.table/SanitizeIdentifier; the remaining SQL fragments are fixed constants.
	query := fmt.Sprintf(`
		SELECT %s, COALESCE(%s, ''), COALESCE(alias, ''), COALESCE(langcode, ''), %s
		FROM %s
		%s
		ORDER BY %s`, safeID, aliasSourceColumn(r.schema), statusExpr, tbl, whereClause, safeID)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query path aliases: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var aliases []PathAlias
	for rows.Next() {
		var a PathAlias
		if err := rows.Scan(&a.ID, &a.Path, &a.Alias, &a.Langcode, &a.Status); err != nil {
			return nil, fmt.Errorf("failed to scan path alias: %w", err)
		}
		aliases = append(aliases, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating path aliases: %w", err)
	}
	return aliases, nil
}

// GetMenuLinks returns each custom menu link's source translation. Drupal's
// default_langcode flag is per entity, so these rows can belong to several site
// languages and the langcode must travel with the link for alias resolution.
func (r *Reader) GetMenuLinks(ctx context.Context) ([]MenuLink, error) {
	if !r.schema.HasMenuLinks {
		return nil, nil
	}
	dataTbl, err := r.table(tableMenuLinkData)
	if err != nil {
		return nil, err
	}
	entityTbl, err := r.table("menu_link_content")
	if err != nil {
		return nil, err
	}

	// #nosec G201 -- both table names are rebuilt by Reader.table from a sanitized prefix and constant suffixes.
	query := fmt.Sprintf(`
		SELECT d.id, COALESCE(e.uuid, ''), COALESCE(d.title, ''), COALESCE(d.menu_name, ''),
		       COALESCE(d.langcode, ''), COALESCE(d.link__uri, ''), d.parent,
		       COALESCE(d.weight, 0), COALESCE(d.enabled, 1)
		FROM %s d
		LEFT JOIN %s e ON e.id = d.id
		WHERE d.default_langcode = 1
		ORDER BY d.menu_name, d.weight, d.id`, dataTbl, entityTbl)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query menu links: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var links []MenuLink
	for rows.Next() {
		var m MenuLink
		if err := rows.Scan(&m.ID, &m.UUID, &m.Title, &m.MenuName,
			&m.Langcode, &m.LinkURI, &m.Parent, &m.Weight, &m.Enabled); err != nil {
			return nil, fmt.Errorf("failed to scan menu link: %w", err)
		}
		links = append(links, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating menu links: %w", err)
	}
	return links, nil
}
