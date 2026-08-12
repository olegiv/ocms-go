// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

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
	tableNodeData     = "node_field_data"
	tableNodeBody     = "node__body"
	tableNodeImage    = "node__field_image"
	tableNodeTags     = "node__field_tags"
	tableTermData     = "taxonomy_term_field_data"
	tableTermParent   = "taxonomy_term__parent"
	tableFileManaged  = "file_managed"
	tableMediaImage   = "media__field_media_image"
	tableUserData     = "users_field_data"
	tablePathAlias    = "path_alias"
	tableMenuLinkData = "menu_link_content_data"
)

// Schema records which optional Drupal tables exist in the source database.
// Field tables are created per configured field, so a site without an image
// field simply has no node__field_image table — that is normal, not an error.
type Schema struct {
	HasNodeImage bool
	HasNodeTags  bool
	HasTermData  bool
	HasTermPar   bool
	HasFiles     bool
	HasMediaImg  bool
	HasAliases   bool
	HasMenuLinks bool
}

// MissingOptional returns the optional tables that were not found, so the admin
// can be told what will be skipped instead of silently getting less content.
func (s Schema) MissingOptional() []string {
	var missing []string
	for _, check := range []struct {
		present bool
		name    string
	}{
		{s.HasNodeImage, tableNodeImage},
		{s.HasNodeTags, tableNodeTags},
		{s.HasTermData, tableTermData},
		{s.HasTermPar, tableTermParent},
		{s.HasFiles, tableFileManaged},
		{s.HasMediaImg, tableMediaImage},
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
}

// BuildDSN assembles a MySQL DSN from the submitted configuration.
//
// It uses mysql.Config.FormatDSN rather than string formatting so that
// passwords containing '@', '/', ':' or '?' are escaped correctly instead of
// silently producing a malformed DSN — and so timeouts are always set.
func BuildDSN(cfg map[string]string) (string, error) {
	port := strings.TrimSpace(cfg["mysql_port"])
	if port == "" {
		port = "3306"
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return "", fmt.Errorf("invalid MySQL port %q", cfg["mysql_port"])
	}

	host := strings.TrimSpace(cfg["mysql_host"])
	if host == "" {
		return "", fmt.Errorf("MySQL host is required")
	}
	if err := shared.CheckDBHostAllowed(host); err != nil {
		return "", err
	}

	database := strings.TrimSpace(cfg["mysql_database"])
	if database == "" {
		return "", fmt.Errorf("MySQL database name is required")
	}

	mycfg := mysql.NewConfig()
	mycfg.User = cfg["mysql_user"]
	mycfg.Passwd = cfg["mysql_password"]
	mycfg.Net = "tcp"
	mycfg.Addr = fmt.Sprintf("%s:%d", host, portNum)
	mycfg.DBName = database
	mycfg.ParseTime = true
	mycfg.Loc = time.UTC
	mycfg.Timeout = connectTimeout
	mycfg.ReadTimeout = readTimeout
	mycfg.AllowNativePasswords = true
	mycfg.Params = map[string]string{"charset": "utf8mb4"}

	return mycfg.FormatDSN(), nil
}

// NewReader opens a connection to the Drupal database and detects its schema.
func NewReader(ctx context.Context, dsn, tablePrefix string) (*Reader, error) {
	if _, err := shared.SanitizeTablePrefix(tablePrefix); err != nil {
		return nil, fmt.Errorf("invalid table prefix: %w", err)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
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

	r := &Reader{db: db, prefix: tablePrefix}
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
	return fmt.Sprintf("`%s%s`", safePrefix, name), nil
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

	r.schema = Schema{
		HasNodeImage: has(tableNodeImage),
		HasNodeTags:  has(tableNodeTags),
		HasTermData:  has(tableTermData),
		HasTermPar:   has(tableTermParent),
		HasFiles:     has(tableFileManaged),
		HasMediaImg:  has(tableMediaImage),
		HasAliases:   has(tablePathAlias),
		HasMenuLinks: has(tableMenuLinkData),
	}
	return nil
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
		// not cost the admin their entire media import.
		slog.Warn("failed to read drupal media alt text", "error", err)
		return files, nil
	}
	for i := range files {
		if alt, ok := alts[files[i].FID]; ok {
			files[i].Alt = sql.NullString{String: alt, Valid: true}
		}
	}
	return files, nil
}

// fileAltText maps file ID to alt text from the image media field.
func (r *Reader) fileAltText(ctx context.Context) (map[int64]string, error) {
	alts := make(map[int64]string)
	if !r.schema.HasMediaImg {
		return alts, nil
	}
	tbl, err := r.table(tableMediaImage)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		SELECT field_media_image_target_id, COALESCE(field_media_image_alt, '')
		FROM %s
		WHERE field_media_image_target_id IS NOT NULL`, tbl)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query media image field: %w", err)
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
			return nil, fmt.Errorf("failed to scan media image field: %w", err)
		}
		if alt != "" {
			alts[fid] = alt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating media image field: %w", err)
	}
	return alts, nil
}

// GetNodes returns one batch of default-language nodes ordered by nid, with
// body, image and tag references joined in.
func (r *Reader) GetNodes(ctx context.Context, offset int) ([]Node, error) {
	nodeTbl, err := r.table(tableNodeData)
	if err != nil {
		return nil, err
	}
	bodyTbl, err := r.table(tableNodeBody)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		SELECT n.nid, n.type, COALESCE(n.langcode, ''), COALESCE(n.title, ''),
		       COALESCE(n.status, 0), COALESCE(n.uid, 0),
		       COALESCE(n.created, 0), COALESCE(n.changed, 0),
		       b.body_value, b.body_summary, b.body_format
		FROM %s n
		LEFT JOIN %s b ON b.entity_id = n.nid AND b.langcode = n.langcode AND b.delta = 0
		WHERE n.default_langcode = 1
		ORDER BY n.nid
		LIMIT ? OFFSET ?`, nodeTbl, bodyTbl)

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

// NodeImages maps node ID to its featured-image file ID and alt text.
func (r *Reader) NodeImages(ctx context.Context) (map[int64]int64, map[int64]string, error) {
	images := make(map[int64]int64)
	alts := make(map[int64]string)
	if !r.schema.HasNodeImage {
		return images, alts, nil
	}
	tbl, err := r.table(tableNodeImage)
	if err != nil {
		return nil, nil, err
	}

	query := fmt.Sprintf(`
		SELECT entity_id, field_image_target_id, COALESCE(field_image_alt, '')
		FROM %s
		WHERE delta = 0 AND field_image_target_id IS NOT NULL`, tbl)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query node images: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var nid, fid int64
		var alt string
		if err := rows.Scan(&nid, &fid, &alt); err != nil {
			return nil, nil, fmt.Errorf("failed to scan node image: %w", err)
		}
		images[nid] = fid
		if alt != "" {
			alts[nid] = alt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("error iterating node images: %w", err)
	}
	return images, alts, nil
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

	query := fmt.Sprintf(`
		SELECT entity_id, field_tags_target_id
		FROM %s
		WHERE field_tags_target_id IS NOT NULL
		ORDER BY entity_id, delta`, tbl)

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

// GetPathAliases returns published path aliases in the default language.
func (r *Reader) GetPathAliases(ctx context.Context) ([]PathAlias, error) {
	if !r.schema.HasAliases {
		return nil, nil
	}
	tbl, err := r.table(tablePathAlias)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		SELECT id, COALESCE(path, ''), COALESCE(alias, ''), COALESCE(langcode, ''), COALESCE(status, 1)
		FROM %s
		WHERE status = 1
		ORDER BY id`, tbl)

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
