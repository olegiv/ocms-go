package api

import (
	"database/sql"
	"testing"
	"time"

	"ocms-go/internal/store"
)

func TestStoreCategoryToResponse(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		category store.Category
		want     CategoryResponse
	}{
		{
			name: "category with description",
			category: store.Category{
				ID:          1,
				Name:        "Tech",
				Slug:        "tech",
				Description: sql.NullString{String: "Technology articles", Valid: true},
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			want: CategoryResponse{
				ID:          1,
				Name:        "Tech",
				Slug:        "tech",
				Description: "Technology articles",
			},
		},
		{
			name: "category without description",
			category: store.Category{
				ID:          2,
				Name:        "News",
				Slug:        "news",
				Description: sql.NullString{Valid: false},
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			want: CategoryResponse{
				ID:          2,
				Name:        "News",
				Slug:        "news",
				Description: "",
			},
		},
		{
			name: "category with empty description",
			category: store.Category{
				ID:          3,
				Name:        "Blog",
				Slug:        "blog",
				Description: sql.NullString{String: "", Valid: true},
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			want: CategoryResponse{
				ID:          3,
				Name:        "Blog",
				Slug:        "blog",
				Description: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storeCategoryToResponse(tt.category)

			if got.ID != tt.want.ID {
				t.Errorf("ID = %d, want %d", got.ID, tt.want.ID)
			}
			if got.Name != tt.want.Name {
				t.Errorf("Name = %q, want %q", got.Name, tt.want.Name)
			}
			if got.Slug != tt.want.Slug {
				t.Errorf("Slug = %q, want %q", got.Slug, tt.want.Slug)
			}
			if got.Description != tt.want.Description {
				t.Errorf("Description = %q, want %q", got.Description, tt.want.Description)
			}
		})
	}
}

func TestStoreTagToResponse(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		tag  store.Tag
		want TagResponse
	}{
		{
			name: "basic tag",
			tag: store.Tag{
				ID:        1,
				Name:      "golang",
				Slug:      "golang",
				CreatedAt: now,
				UpdatedAt: now,
			},
			want: TagResponse{
				ID:   1,
				Name: "golang",
				Slug: "golang",
			},
		},
		{
			name: "tag with special characters in name",
			tag: store.Tag{
				ID:        2,
				Name:      "C++",
				Slug:      "cpp",
				CreatedAt: now,
				UpdatedAt: now,
			},
			want: TagResponse{
				ID:   2,
				Name: "C++",
				Slug: "cpp",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storeTagToResponse(tt.tag)

			if got.ID != tt.want.ID {
				t.Errorf("ID = %d, want %d", got.ID, tt.want.ID)
			}
			if got.Name != tt.want.Name {
				t.Errorf("Name = %q, want %q", got.Name, tt.want.Name)
			}
			if got.Slug != tt.want.Slug {
				t.Errorf("Slug = %q, want %q", got.Slug, tt.want.Slug)
			}
		})
	}
}
