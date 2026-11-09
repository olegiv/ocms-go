// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

// DBHandle exposes the existing database/transaction to the shared page-write
// boundary. Callers continue to execute statements through generated queries.
func (q *Queries) DBHandle() DBTX { return q.db }
