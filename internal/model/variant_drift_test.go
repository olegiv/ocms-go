// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

// TestNoHardcodedVariantLists fails on any string-literal slice that enumerates
// image variant directories instead of deriving them from model.ImageVariants.
//
// This is the guard the codebase kept needing and never had. Four separate
// cleanup paths each grew their own copy of the list, and two of them had
// already drifted: modules/migrator's deleteMediaFiles and the Drupal source's
// removeOrphanedUpload both omitted "og", so every imported image left an
// orphaned /uploads/og/<uuid> — in removeOrphanedUpload's case on the exact
// path that exists to clean up after a failed insert.
//
// Fixing the copies alone would leave the fifth free to reintroduce it, which
// is the whole reason this is expressed over the source tree rather than at the
// sites that broke.
//
// Bug state: restore any of those literals and this names the file and line.
func TestNoHardcodedVariantLists(t *testing.T) {
	variantNames := make(map[string]bool, len(model.ImageVariants)+1)
	for variant := range model.ImageVariants {
		variantNames[variant] = true
	}
	variantNames[model.OriginalsDir] = true

	repoRoot := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var offenders []string

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "wiki", "bin", "data", "uploads":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// model owns the canonical list, so its own literals are the point.
		if filepath.Dir(path) == filepath.Join(repoRoot, "internal", "model") {
			return nil
		}

		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil //nolint:nilerr // unparseable files are the compiler's problem
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			matched := 0
			for _, elt := range lit.Elts {
				basic, ok := elt.(*ast.BasicLit)
				if !ok || basic.Kind != token.STRING {
					continue
				}
				value, uerr := strconv.Unquote(basic.Value)
				if uerr != nil {
					continue
				}
				if variantNames[value] {
					matched++
				}
			}
			// Two or more is an enumeration of the variant set, not a passing
			// mention of one directory name.
			if matched >= 2 {
				rel, rerr := filepath.Rel(repoRoot, fset.Position(lit.Pos()).Filename)
				if rerr != nil {
					rel = fset.Position(lit.Pos()).Filename
				}
				offenders = append(offenders,
					rel+":"+strconv.Itoa(fset.Position(lit.Pos()).Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking repository: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("these literals enumerate image variant directories by hand; "+
			"use model.MediaStorageDirs() (or range model.ImageVariants) so a new "+
			"variant cannot be created without also being deleted:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
