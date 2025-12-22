package model

import (
	"testing"
)

func TestUserIsAdmin(t *testing.T) {
	tests := []struct {
		name string
		role string
		want bool
	}{
		{
			name: "admin role",
			role: RoleAdmin,
			want: true,
		},
		{
			name: "editor role",
			role: "editor",
			want: false,
		},
		{
			name: "user role",
			role: "user",
			want: false,
		},
		{
			name: "empty role",
			role: "",
			want: false,
		},
		{
			name: "Admin uppercase",
			role: "Admin",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &User{Role: tt.role}
			if got := u.IsAdmin(); got != tt.want {
				t.Errorf("IsAdmin() = %v, want %v", got, tt.want)
			}
		})
	}
}
