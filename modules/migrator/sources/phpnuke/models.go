// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

import (
	"database/sql"
	"time"
)

// Story is a news article from the PHP-Nuke `stories` table.
//
// PHP-Nuke splits an article body across two columns: hometext is the teaser
// shown on the front page and bodytext is the remainder shown after "read
// more". Either may be empty, and a short article often lives entirely in
// hometext with bodytext left blank.
type Story struct {
	ID         int64          // sid
	CategoryID int64          // catid, 0 when uncategorized
	AuthorID   string         // aid, joins stories.aid to authors.aid
	Title      sql.NullString // title
	Time       sql.NullTime   // time
	HomeText   sql.NullString // hometext (teaser)
	BodyText   string         // bodytext (remainder)
	TopicID    int64          // topic
	Informant  string         // informant, the submitting username
	Notes      string         // notes
}

// StaticPage is a page from the PHP-Nuke `pages` table.
type StaticPage struct {
	ID         int64  // pid
	CategoryID int64  // cid
	Title      string // title
	Subtitle   string // subtitle
	Active     int64  // active
	Header     string // page_header
	Text       string // text
	Footer     string // page_footer
	Signature  string // signature
	Date       sql.NullTime
}

// IsActive reports whether the page was published on the source site.
func (p *StaticPage) IsActive() bool {
	return p.Active != 0
}

// Topic is a thematic topic from the PHP-Nuke `topics` table. Topics are the
// primary story taxonomy and become oCMS categories.
type Topic struct {
	ID    int64          // topicid
	Name  sql.NullString // topicname, a short machine-ish name
	Text  sql.NullString // topictext, the human-readable label
	Image string         // topicimage
}

// Label returns the best available human-readable topic name.
func (t *Topic) Label() string {
	if t.Text.Valid && t.Text.String != "" {
		return t.Text.String
	}
	if t.Name.Valid {
		return t.Name.String
	}
	return ""
}

// Category is a story category from `stories_cat`, or a page category from
// `pages_categories`. Both tables carry the same shape.
type Category struct {
	ID    int64  // catid / cid
	Title string // title
}

// EncyclopediaEntry is one encyclopedia from the PHP-Nuke `encyclopedia`
// table. An entry is a container: its terms live in `encyclopedia_text`.
type EncyclopediaEntry struct {
	ID          int64  // eid
	Title       string // title
	Description string // description
	Active      int64  // active
}

// IsActive reports whether the encyclopedia was published on the source site.
func (e *EncyclopediaEntry) IsActive() bool {
	return e.Active != 0
}

// EncyclopediaTerm is a single definition inside an encyclopedia.
type EncyclopediaTerm struct {
	ID      int64  // tid
	EntryID int64  // eid
	Title   string // title
	Text    string // text
}

// User is an account from the PHP-Nuke `users` table.
type User struct {
	ID       int64  // user_id
	Username string // username
	Name     string // name
	Email    string // user_email
}

// DisplayName returns the best available human-readable name, falling back to
// the username when the profile name is blank.
func (u *User) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Username
}

// storyTimestamp returns the story's publication time, or now when the source
// row has no usable timestamp.
func storyTimestamp(s *Story, now time.Time) time.Time {
	if s.Time.Valid && !s.Time.Time.IsZero() {
		return s.Time.Time
	}
	return now
}
