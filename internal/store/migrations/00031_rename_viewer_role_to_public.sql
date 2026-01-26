-- Copyright (c) 2025-2026 Oleg Ivanchenko
-- SPDX-License-Identifier: GPL-3.0-or-later

-- +goose Up
-- +goose StatementBegin
-- Rename "viewer" role to "public" for existing users
UPDATE users SET role = 'public' WHERE role = 'viewer';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Revert "public" role back to "viewer"
UPDATE users SET role = 'viewer' WHERE role = 'public';
-- +goose StatementEnd
