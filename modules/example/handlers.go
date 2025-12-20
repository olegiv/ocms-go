package example

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
)

// ExampleItem represents an item in the example module.
type ExampleItem struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// handleExample handles GET /example - public route.
func (m *Module) handleExample(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <title>Example Module</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            max-width: 800px;
            margin: 50px auto;
            padding: 20px;
            background: #f5f5f5;
        }
        .card {
            background: white;
            border-radius: 8px;
            padding: 30px;
            box-shadow: 0 2px 10px rgba(0,0,0,0.1);
        }
        h1 { color: #333; margin-top: 0; }
        p { color: #666; line-height: 1.6; }
        .badge {
            display: inline-block;
            padding: 4px 12px;
            background: #4CAF50;
            color: white;
            border-radius: 20px;
            font-size: 14px;
        }
        a { color: #1976D2; }
    </style>
</head>
<body>
    <div class="card">
        <h1>Example Module <span class="badge">v1.0.0</span></h1>
        <p>This is the public page of the example module. It demonstrates how modules can register public routes.</p>
        <p>The example module provides:</p>
        <ul>
            <li>Public route at <code>/example</code></li>
            <li>Admin routes at <code>/admin/example</code></li>
            <li>Template functions: <code>exampleFunc</code>, <code>exampleVersion</code></li>
            <li>Hook handlers for page events</li>
            <li>Database migration for the <code>example_items</code> table</li>
        </ul>
        <p><a href="/">← Back to Home</a> | <a href="/admin/example">Admin Page →</a></p>
    </div>
</body>
</html>`))
}

// handleAdminExample handles GET /admin/example - admin route.
func (m *Module) handleAdminExample(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	// Fetch items from the database
	items, err := m.listItems()
	if err != nil {
		m.ctx.Logger.Error("failed to list example items", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Items   []ExampleItem
		Version string
	}{
		Items:   items,
		Version: m.Version(),
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_example", render.TemplateData{
		Title: i18n.T(lang, "example.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "example.title"), URL: "/admin/example", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleListItems handles GET /admin/example/items - returns JSON list of items.
func (m *Module) handleListItems(w http.ResponseWriter, _ *http.Request) {
	items, err := m.listItems()
	if err != nil {
		m.ctx.Logger.Error("failed to list example items", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": items,
		"total": len(items),
	})
}

// handleCreateItem handles POST /admin/example/items - creates a new item.
func (m *Module) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	description := r.FormValue("description")

	if name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	item, err := m.createItem(name, description)
	if err != nil {
		m.ctx.Logger.Error("failed to create example item", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Check if this is an AJAX request
	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(item)
		return
	}

	// Redirect back to the admin page
	http.Redirect(w, r, "/admin/example", http.StatusSeeOther)
}

// handleDeleteItem handles DELETE /admin/example/items/{id} - deletes an item.
func (m *Module) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	if err := m.deleteItem(id); err != nil {
		m.ctx.Logger.Error("failed to delete example item", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Database operations

func (m *Module) listItems() ([]ExampleItem, error) {
	rows, err := m.ctx.DB.Query(`
		SELECT id, name, description, created_at
		FROM example_items
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []ExampleItem
	for rows.Next() {
		var item ExampleItem
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	return items, rows.Err()
}

func (m *Module) createItem(name, description string) (*ExampleItem, error) {
	result, err := m.ctx.DB.Exec(`
		INSERT INTO example_items (name, description, created_at)
		VALUES (?, ?, ?)
	`, name, description, time.Now())
	if err != nil {
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return &ExampleItem{
		ID:          id,
		Name:        name,
		Description: description,
		CreatedAt:   time.Now(),
	}, nil
}

func (m *Module) deleteItem(id int64) error {
	result, err := m.ctx.DB.Exec(`DELETE FROM example_items WHERE id = ?`, id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}
