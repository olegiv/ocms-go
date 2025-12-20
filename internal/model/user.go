// Package model defines domain models and types used throughout the application
// including User, Page, Event, and configuration structures.
package model

import (
	"database/sql"
	"time"
)

// User roles
const (
	RoleAdmin = "admin"
)

// User represents a CMS user.
type User struct {
	ID           int64        `json:"id"`
	Email        string       `json:"email"`
	PasswordHash string       `json:"-"` // Never expose in JSON
	Role         string       `json:"role"`
	Name         string       `json:"name"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	LastLoginAt  sql.NullTime `json:"last_login_at,omitempty"`
}

// IsAdmin returns true if the user has admin role.
func (u *User) IsAdmin() bool {
	return u.Role == RoleAdmin
}
