// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Setting is a value a site may set in its .env: Env names the key, and
// Default applies when the file leaves it out or empty. A relative path is
// relative to the site directory, as the application's working directory is.
// Name identifies the setting in what the server reports (`site paths`) and
// in backup archive names.
type Setting struct {
	Name    string // e.g. uploads
	Env     string // e.g. OCMS_UPLOADS_DIR; empty: always Default
	Default string // e.g. uploads
}

// value returns the setting from lookup, or its default.
func (s Setting) value(lookup func(string) (string, bool)) string {
	if s.Env != "" {
		if v, ok := lookup(s.Env); ok && v != "" {
			return v
		}
	}
	return s.Default
}

// path returns the setting as an absolute path under dir (itself absolute);
// "" when it is unset and has no default.
func (s Setting) path(dir string, lookup func(string) (string, bool)) string {
	value := s.value(lookup)
	if value == "" {
		return ""
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(dir, value)
	}
	return filepath.Clean(value)
}

// sitePaths are a site's configured paths, absolute; ErrorLog is "" when the
// site keeps no error log.
type sitePaths struct{ DB, Uploads, Custom, ErrorLog string }

type pathField struct {
	setting Setting
	value   *string
}

func (a *App) pathFields(p *sitePaths) []pathField {
	return []pathField{{a.DB, &p.DB}, {a.Uploads, &p.Uploads}, {a.Custom, &p.Custom}, {a.ErrorLog, &p.ErrorLog}}
}

func (a *App) resolvePaths(dir string, lookup func(string) (string, bool)) sitePaths {
	var p sitePaths
	for _, f := range a.pathFields(&p) {
		*f.value = f.setting.path(dir, lookup)
	}
	return p
}

func (p sitePaths) themes(a *App) string { return filepath.Join(p.Custom, a.ThemesPath) }

// within reports whether path is dir or below it; both are absolute and clean.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// resolved returns the absolute path with the symlinks in its existing part
// resolved, so two names of one directory compare equal.
func resolved(path string) string {
	missing := ""
	for dir := filepath.Clean(path); ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, missing)
		}
		if dir == filepath.Dir(dir) {
			return path
		}
		missing = filepath.Join(filepath.Base(dir), missing)
	}
}

// readSitePaths reads the site's .env without executing it, with the
// caller's rights. Only SiteOp calls it, so a site's configuration is read
// as the site user, never as root.
func (a *App) readSitePaths(dir string) (sitePaths, error) {
	values, err := godotenv.Read(filepath.Join(dir, ".env"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return sitePaths{}, fmt.Errorf("site configuration: %w", err)
	}
	return a.resolvePaths(dir, func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}), nil
}

// checkPath refuses a path that cannot cross ssh as one word, or that is not
// an absolute, clean path below the root.
func checkPath(path string) error {
	if err := checkArgs(path); err != nil {
		return err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return fmt.Errorf("%q is not an absolute directory or file below /", path)
	}
	return nil
}

// writePaths prints the site's paths for `site paths`, one "<name> <path>"
// line each. pull hands them to the remote shell and logs to tail, so a
// path that could not cross ssh as one word is an error here, at its source.
func (a *App) writePaths(w io.Writer, p sitePaths) error {
	for _, f := range a.pathFields(&p) {
		if *f.value == "" {
			continue
		}
		if err := checkPath(*f.value); err != nil {
			return fmt.Errorf("%s: %w (letters, digits and ._@:/=- only)", f.setting.Env, err)
		}
		if _, err := fmt.Fprintf(w, "%s %s\n", f.setting.Name, *f.value); err != nil {
			return err
		}
	}
	return nil
}

// parsePaths reads what writePaths printed. The values come from a site's
// own configuration, so each is checked again: one value per name, an
// absolute clean path, safe for the remote shell. A setting with a default
// must be present.
func (a *App) parsePaths(text string) (sitePaths, error) {
	var p sitePaths
	fields := a.pathFields(&p)
	seen := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		name, value, _ := strings.Cut(scanner.Text(), " ")
		for _, f := range fields {
			if f.setting.Name != name {
				continue
			}
			if seen[name] {
				return sitePaths{}, fmt.Errorf("the server reported %s twice", name)
			}
			seen[name] = true
			if err := checkPath(value); err != nil {
				return sitePaths{}, fmt.Errorf("the server reported %s %w", name, err)
			}
			*f.value = value
		}
	}
	for _, f := range fields {
		if f.setting.Default != "" && *f.value == "" {
			return sitePaths{}, fmt.Errorf("the server did not report %s", f.setting.Name)
		}
	}
	return p, scanner.Err()
}

// SitePaths prints a site's configured paths (`site paths`), read as the
// site user.
func (a *App) SitePaths(ctx context.Context, site Site) error {
	return a.asSiteUser(ctx, site, "paths", nil, a.Stdout)
}

// localConfig is a development checkout's configuration, absolute.
type localConfig struct {
	sitePaths
	Port    int
	Dir     string
	Sources map[string]string // setting name -> where its value came from
}

// readLocalConfig resolves the configuration of the checkout in dir as its
// own server resolves it: the process environment first, then the env file
// (the one EnvFileEnv names, or dir/.env when there is one), then the
// defaults. A file that cannot be read or a port that is not one is an
// error: guessing would aim pull at a database or port the server does not
// use.
func (a *App) readLocalConfig(dir string) (localConfig, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return localConfig{}, err
	}
	lookupEnv := a.LookupEnv
	if lookupEnv == nil {
		lookupEnv = func(string) (string, bool) { return "", false }
	}
	file, named := "", false
	if a.EnvFileEnv != "" {
		file, named = lookupEnv(a.EnvFileEnv)
		named = named && file != ""
	}
	if !named {
		file = ".env"
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(dir, file)
	}
	values, err := godotenv.Read(file)
	if err != nil && (named || !errors.Is(err, os.ErrNotExist)) {
		return localConfig{}, fmt.Errorf("local configuration: %w", err)
	}
	label := "the environment"
	from := map[string]string{} // key -> label of the value lookup returned
	lookup := func(key string) (string, bool) {
		if v, ok := lookupEnv(key); ok {
			from[key] = label
			return v, true
		}
		v, ok := values[key]
		from[key] = filepath.Base(file)
		return v, ok
	}
	c := localConfig{sitePaths: a.resolvePaths(dir, lookup), Dir: dir, Sources: map[string]string{}}
	for _, s := range []Setting{a.DB, a.Uploads, a.Custom, a.Port} {
		c.Sources[s.Name] = "default"
		if v, ok := lookup(s.Env); s.Env != "" && ok && v != "" {
			c.Sources[s.Name] = from[s.Env]
		}
	}
	if value := a.Port.value(lookup); value != "" {
		if c.Port, err = strconv.Atoi(value); err != nil || c.Port < 1 || c.Port > 65535 {
			return localConfig{}, fmt.Errorf("local configuration: %s=%q is not a port", a.Port.Env, value)
		}
	}
	return c, nil
}
