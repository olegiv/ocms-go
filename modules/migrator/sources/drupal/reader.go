// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"maps"
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

	query := fmt.Sprintf(`
		SELECT tid, COALESCE(vid, ''), COALESCE(name, ''), description__value, COALESCE(weight, 0)
		FROM %s
		WHERE default_langcode = 1
		ORDER BY vid, weight, tid`, tbl)

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
		if err := rows.Scan(&t.TID, &t.Vocabulary, &t.Name, &t.Description, &t.Weight); err != nil {
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

	query := fmt.Sprintf("SELECT entity_id, parent_target_id FROM %s", tbl)
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query taxonomy hierarchy: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var child, parent int64
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, fmt.Errorf("failed to scan taxonomy hierarchy: %w", err)
		}
		if parent != 0 {
			parents[child] = parent
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating taxonomy hierarchy: %w", err)
	}
	return parents, nil
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

// buildAltQuery assembles a "(file id, alt text)" query over an image field
// table. Both shapes are identical apart from their column names, so they share
// one builder rather than two near-copies.
//
// Column names cannot be bound as parameters, so they are validated here rather
// than trusted from the caller. Every caller currently passes a literal, which
// is what kept this safe; validating makes that a property of the function
// instead of a property of the call sites.
func buildAltQuery(tbl, idColumn, altColumn string) (string, error) {
	safeID, err := shared.SanitizeIdentifier(idColumn)
	if err != nil {
		return "", err
	}
	safeAlt, err := shared.SanitizeIdentifier(altColumn)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`
		SELECT %s, COALESCE(%s, '')
		FROM %s
		WHERE %s IS NOT NULL`, safeID, safeAlt, tbl, safeID), nil
}

// readAltMap runs an alt-text query and returns file ID -> first non-empty alt.
func (r *Reader) readAltMap(ctx context.Context, table, idColumn, altColumn string) (map[int64]string, error) {
	alts := make(map[int64]string)
	tbl, err := r.table(table)
	if err != nil {
		return nil, err
	}

	query, err := buildAltQuery(tbl, idColumn, altColumn)
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
		var fid int64
		var alt string
		if err := rows.Scan(&fid, &alt); err != nil {
			return nil, fmt.Errorf("failed to scan %s: %w", table, err)
		}
		if alt != "" {
			if _, seen := alts[fid]; !seen {
				alts[fid] = alt
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating %s: %w", table, err)
	}
	return alts, nil
}

// mediaFileFieldTables lists the media__field_media_* tables that reference a
// file_managed row. A site has whichever tables its media bundles declare, so
// each is probed rather than assumed.
var mediaFileFieldTables = []struct{ table, column string }{
	{"media__field_media_image", "field_media_image_target_id"},
	{"media__field_media_document", "field_media_document_target_id"},
	{"media__field_media_file", "field_media_file_target_id"},
	{"media__field_media_audio_file", "field_media_audio_file_target_id"},
	{"media__field_media_video_file", "field_media_video_file_target_id"},
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
	mediaTbl, err := r.table(tableMedia)
	if err != nil {
		return nil, err
	}

	for _, field := range mediaFileFieldTables {
		if !r.hasTable(field.table) {
			continue
		}
		fieldTbl, err := r.table(field.table)
		if err != nil {
			return nil, err
		}

		if err := r.collectMediaUUIDs(ctx, mediaTbl, fieldTbl, field.column, field.table, byFile); err != nil {
			return nil, err
		}
	}
	return byFile, nil
}

// collectMediaUUIDs adds one media field table's file references to byFile.
func (r *Reader) collectMediaUUIDs(ctx context.Context, mediaTbl, fieldTbl, column, label string,
	byFile map[int64][]string) error {

	// As in buildAltQuery: the column name is interpolated, so validate it here
	// rather than relying on every caller to pass a literal.
	safeColumn, err := shared.SanitizeIdentifier(column)
	if err != nil {
		return err
	}

	query := fmt.Sprintf(`
		SELECT m.uuid, f.%s
		FROM %s m
		JOIN %s f ON f.entity_id = m.mid
		WHERE f.%s IS NOT NULL AND m.uuid <> ''`, safeColumn, mediaTbl, fieldTbl, safeColumn)

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
// Drupal's alt is per field instance, so two nodes may describe one shared file
// differently; oCMS stores one alt per media row, so the first non-empty value
// wins. On the 1:1 sites this fallback exists for, there is nothing to lose.
func (r *Reader) fileAltText(ctx context.Context) (map[int64]string, error) {
	alts := make(map[int64]string)

	if r.schema.HasNodeImage {
		nodeAlts, err := r.readAltMap(ctx, tableNodeImage, "field_image_target_id", "field_image_alt")
		if err != nil {
			return nil, err
		}
		maps.Copy(alts, nodeAlts)
	}

	if r.schema.HasMediaImg {
		mediaAlts, err := r.readAltMap(ctx, tableMediaImage,
			"field_media_image_target_id", "field_media_image_alt")
		if err != nil {
			return nil, err
		}
		maps.Copy(alts, mediaAlts)
	}

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
		LEFT JOIN %s b ON b.entity_id = n.nid AND b.langcode = n.langcode AND b.delta = 0
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
	query := fmt.Sprintf(`
		SELECT f.entity_id, f.field_image_target_id
		FROM %s f
		JOIN %s n ON n.nid = f.entity_id AND n.default_langcode = 1 AND f.langcode = n.langcode
		WHERE f.delta = 0 AND f.field_image_target_id IS NOT NULL`, tbl, nodeTbl)

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

// NodeTerms maps node ID to the taxonomy term IDs it references.
func (r *Reader) NodeTerms(ctx context.Context) (map[int64][]int64, error) {
	terms := make(map[int64][]int64)
	if !r.schema.HasNodeTags {
		return terms, nil
	}
	tbl, err := r.table(tableNodeTags)
	if err != nil {
		return nil, err
	}

	nodeTbl, err := r.table(tableNodeData)
	if err != nil {
		return nil, err
	}

	// As with NodeImages: unfiltered, this appended the terms of every
	// translation onto the default-language page.
	query := fmt.Sprintf(`
		SELECT f.entity_id, f.field_tags_target_id
		FROM %s f
		JOIN %s n ON n.nid = f.entity_id AND n.default_langcode = 1 AND f.langcode = n.langcode
		WHERE f.field_tags_target_id IS NOT NULL
		ORDER BY f.entity_id, f.delta`, tbl, nodeTbl)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query node tags: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var nid, tid int64
		if err := rows.Scan(&nid, &tid); err != nil {
			return nil, fmt.Errorf("failed to scan node tag: %w", err)
		}
		terms[nid] = append(terms[nid], tid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node tags: %w", err)
	}
	return terms, nil
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

// GetMenuLinks returns custom menu links in the default language.
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

	query := fmt.Sprintf(`
		SELECT d.id, COALESCE(e.uuid, ''), COALESCE(d.title, ''), COALESCE(d.menu_name, ''),
		       COALESCE(d.link__uri, ''), d.parent, COALESCE(d.weight, 0), COALESCE(d.enabled, 1)
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
			&m.LinkURI, &m.Parent, &m.Weight, &m.Enabled); err != nil {
			return nil, fmt.Errorf("failed to scan menu link: %w", err)
		}
		links = append(links, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating menu links: %w", err)
	}
	return links, nil
}
