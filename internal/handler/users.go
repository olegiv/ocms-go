package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/auth"
	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
)

// User roles
const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// ValidRoles contains all valid user roles.
var ValidRoles = []string{RoleAdmin, RoleEditor, RoleViewer}

// UsersPerPage is the number of users to display per page.
const UsersPerPage = 10

// UsersHandler handles user management routes.
type UsersHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewUsersHandler creates a new UsersHandler.
func NewUsersHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *UsersHandler {
	return &UsersHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// UsersListData holds data for the users list template.
type UsersListData struct {
	Users       []store.User
	CurrentPage int
	TotalPages  int
	TotalUsers  int64
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
}

// List handles GET /admin/users - displays a paginated list of users.
func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Get page number from query string
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get total user count
	totalUsers, err := h.queries.CountUsers(r.Context())
	if err != nil {
		slog.Error("failed to count users", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Calculate pagination
	totalPages := int((totalUsers + UsersPerPage - 1) / UsersPerPage)
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := int64((page - 1) * UsersPerPage)

	// Fetch users for current page
	users, err := h.queries.ListUsers(r.Context(), store.ListUsersParams{
		Limit:  UsersPerPage,
		Offset: offset,
	})
	if err != nil {
		slog.Error("failed to list users", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := UsersListData{
		Users:       users,
		CurrentPage: page,
		TotalPages:  totalPages,
		TotalUsers:  totalUsers,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
	}

	if err := h.renderer.Render(w, r, "admin/users_list", render.TemplateData{
		Title: "Users",
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// UserFormData holds data for the user form template.
type UserFormData struct {
	User       *store.User
	Roles      []string
	Errors     map[string]string
	FormValues map[string]string
	IsEdit     bool
}

// NewForm handles GET /admin/users/new - displays the new user form.
func (h *UsersHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	data := UserFormData{
		Roles:      ValidRoles,
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     false,
	}

	if err := h.renderer.Render(w, r, "admin/users_form", render.TemplateData{
		Title: "New User",
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Create handles POST /admin/users - creates a new user.
func (h *UsersHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/users/new", http.StatusSeeOther)
		return
	}

	// Get form values
	email := strings.TrimSpace(r.FormValue("email"))
	name := strings.TrimSpace(r.FormValue("name"))
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")
	role := r.FormValue("role")

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"email": email,
		"name":  name,
		"role":  role,
	}

	// Validate
	errors := make(map[string]string)

	// Email validation
	if email == "" {
		errors["email"] = "Email is required"
	} else if _, err := mail.ParseAddress(email); err != nil {
		errors["email"] = "Invalid email format"
	} else {
		// Check if email already exists
		_, err := h.queries.GetUserByEmail(r.Context(), email)
		if err == nil {
			errors["email"] = "Email already exists"
		} else if err != sql.ErrNoRows {
			slog.Error("database error checking email", "error", err)
			errors["email"] = "Error checking email"
		}
	}

	// Name validation
	if name == "" {
		errors["name"] = "Name is required"
	} else if len(name) < 2 {
		errors["name"] = "Name must be at least 2 characters"
	}

	// Password validation
	if password == "" {
		errors["password"] = "Password is required"
	} else if len(password) < 8 {
		errors["password"] = "Password must be at least 8 characters"
	} else if password != passwordConfirm {
		errors["password_confirm"] = "Passwords do not match"
	}

	// Role validation
	if role == "" {
		errors["role"] = "Role is required"
	} else if !isValidRole(role) {
		errors["role"] = "Invalid role"
	}

	// If there are validation errors, re-render the form
	if len(errors) > 0 {
		data := UserFormData{
			Roles:      ValidRoles,
			Errors:     errors,
			FormValues: formValues,
			IsEdit:     false,
		}

		if err := h.renderer.Render(w, r, "admin/users_form", render.TemplateData{
			Title: "New User",
			User:  user,
			Data:  data,
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Hash password
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		slog.Error("failed to hash password", "error", err)
		h.renderer.SetFlash(r, "Error creating user", "error")
		http.Redirect(w, r, "/admin/users/new", http.StatusSeeOther)
		return
	}

	// Create user
	now := time.Now()
	newUser, err := h.queries.CreateUser(r.Context(), store.CreateUserParams{
		Email:        email,
		PasswordHash: passwordHash,
		Role:         role,
		Name:         name,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to create user", "error", err)
		h.renderer.SetFlash(r, "Error creating user", "error")
		http.Redirect(w, r, "/admin/users/new", http.StatusSeeOther)
		return
	}

	slog.Info("user created", "user_id", newUser.ID, "email", newUser.Email, "created_by", user.ID)
	h.renderer.SetFlash(r, "User created successfully", "success")
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// EditForm handles GET /admin/users/{id} - displays the edit user form.
func (h *UsersHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)

	// Get user ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid user ID", "error")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	// Fetch user
	editUser, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "User not found", "error")
		} else {
			slog.Error("failed to get user", "error", err)
			h.renderer.SetFlash(r, "Error loading user", "error")
		}
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	data := UserFormData{
		User:   &editUser,
		Roles:  ValidRoles,
		Errors: make(map[string]string),
		FormValues: map[string]string{
			"email": editUser.Email,
			"name":  editUser.Name,
			"role":  editUser.Role,
		},
		IsEdit: true,
	}

	if err := h.renderer.Render(w, r, "admin/users_form", render.TemplateData{
		Title: "Edit User",
		User:  currentUser,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// isValidRole checks if a role is valid.
func isValidRole(role string) bool {
	for _, r := range ValidRoles {
		if r == role {
			return true
		}
	}
	return false
}
