// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

import (
	"database/sql"
	"time"

	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
)

// Every non-key column below is modelled as a nullable type, even where the
// reference install declares it NOT NULL.
//
// PHP-Nuke databases are twenty years old and have been through version
// upgrades, hand-written SQL and third-party modules. A single NULL in a column
// scanned as a plain string fails the whole query with "converting NULL to
// string is unsupported", and because the content reads happen after users and
// taxonomy are already written, that aborts an import mid-way. Scanning
// defensively costs an unwrap per field and removes an entire class of
// unrecoverable failure. The Drupal source reaches the same goal from the other
// side, wrapping columns in COALESCE in its SQL; Elefant does neither and has
// the same latent failure.

// Story is a news article from the PHP-Nuke `stories` table.
//
// PHP-Nuke splits an article body across two columns: hometext is the teaser
// shown on the front page and bodytext is the remainder shown after "read
// more". Either may be empty, and a short article often lives entirely in
// hometext with bodytext left blank.
type Story struct {
	ID         int64          // sid
	CategoryID sql.NullInt64  // catid, 0 when uncategorized
	AuthorID   sql.NullString // aid, matched against users.username
	Title      sql.NullString // title
	Time       sql.NullTime   // time
	HomeText   sql.NullString // hometext (teaser)
	BodyText   sql.NullString // bodytext (remainder)
	TopicID    sql.NullInt64  // topic
	Informant  sql.NullString // informant, the submitting username
}

// Category returns the source story category id, 0 when unset.
func (s *Story) Category() int64 { return shared.NullInt64(s.CategoryID) }

// Topic returns the source topic id, 0 when unset.
func (s *Story) Topic() int64 { return shared.NullInt64(s.TopicID) }

// StaticPage is a page from the PHP-Nuke `pages` table.
type StaticPage struct {
	ID         int64          // pid
	CategoryID sql.NullInt64  // cid
	Title      sql.NullString // title
	Subtitle   sql.NullString // subtitle
	Active     sql.NullInt64  // active
	Header     sql.NullString // page_header
	Text       sql.NullString // text
	Footer     sql.NullString // page_footer
	Signature  sql.NullString // signature
	Date       sql.NullTime
}

// IsActive reports whether the page was published on the source site.
func (p *StaticPage) IsActive() bool { return shared.NullInt64(p.Active) != 0 }

// Category returns the source category id, 0 when unset.
func (p *StaticPage) Category() int64 { return shared.NullInt64(p.CategoryID) }

// Topic is a thematic topic from the PHP-Nuke `topics` table. Topics are the
// primary story taxonomy and become oCMS categories.
type Topic struct {
	ID   int64          // topicid
	Name sql.NullString // topicname, a short machine-ish name
	Text sql.NullString // topictext, the human-readable label
}

// Label returns the best available human-readable topic name.
func (t *Topic) Label() string {
	if text := shared.NullString(t.Text); text != "" {
		return text
	}
	return shared.NullString(t.Name)
}

// Category is a story category from `stories_cat`, or a page category from
// `pages_categories`. Both tables carry the same shape.
type Category struct {
	ID    int64          // catid / cid
	Title sql.NullString // title
}

// Name returns the category title as a plain string.
func (c *Category) Name() string { return shared.NullString(c.Title) }

// EncyclopediaEntry is one encyclopedia from the PHP-Nuke `encyclopedia`
// table. An entry is a container: its terms live in `encyclopedia_text`.
type EncyclopediaEntry struct {
	ID          int64          // eid
	Title       sql.NullString // title
	Description sql.NullString // description
	Active      sql.NullInt64  // active
}

// IsActive reports whether the encyclopedia was published on the source site.
func (e *EncyclopediaEntry) IsActive() bool { return shared.NullInt64(e.Active) != 0 }

// Name returns the entry title as a plain string.
func (e *EncyclopediaEntry) Name() string { return shared.NullString(e.Title) }

// Body returns the entry description as a plain string.
func (e *EncyclopediaEntry) Body() string { return shared.NullString(e.Description) }

// EncyclopediaTerm is a single definition inside an encyclopedia.
type EncyclopediaTerm struct {
	ID      int64          // tid
	EntryID sql.NullInt64  // eid
	Title   sql.NullString // title
	Text    sql.NullString // text
}

// Entry returns the owning encyclopedia id.
func (t *EncyclopediaTerm) Entry() int64 { return shared.NullInt64(t.EntryID) }

// User is an account from the PHP-Nuke `users` table.
type User struct {
	ID       int64          // user_id
	Username sql.NullString // username
	Name     sql.NullString // name
	Email    sql.NullString // user_email
}

// Login returns the account's username as a plain string.
func (u *User) Login() string { return shared.NullString(u.Username) }

// Address returns the account's email as a plain string.
func (u *User) Address() string { return shared.NullString(u.Email) }

// DisplayName returns the best available human-readable name, falling back to
// the username when the profile name is blank.
func (u *User) DisplayName() string {
	if name := shared.NullString(u.Name); name != "" {
		return name
	}
	return u.Login()
}

// storyTimestamp returns the story's publication time, or now when the source
// row has no usable timestamp.
func storyTimestamp(s *Story, now time.Time) time.Time {
	if s.Time.Valid && !s.Time.Time.IsZero() {
		return s.Time.Time
	}
	return now
}
