// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Site is one line of the registry: "<id> <dir> <user> <port>".
type Site struct {
	ID   string
	Dir  string
	User string
	Port int
}

func (s Site) String() string { return fmt.Sprintf("%s %s %s %d", s.ID, s.Dir, s.User, s.Port) }

const registryHeader = `# Site registry
# Format: SITE_ID INSTANCE_DIR SYSTEM_USER PORT
# A line starting with # is a disabled site.
`

// parseSite reads one registry line.
func parseSite(line string) (Site, error) {
	f := strings.Fields(line)
	if len(f) != 4 {
		return Site{}, fmt.Errorf("want 4 fields, got %d", len(f))
	}
	port, err := strconv.Atoi(f[3])
	if err != nil || port < 1 || port > 65535 {
		return Site{}, fmt.Errorf("invalid port %q", f[3])
	}
	if !siteIDRe.MatchString(f[0]) {
		return Site{}, fmt.Errorf("invalid site id %q", f[0])
	}
	if !filepath.IsAbs(f[1]) {
		return Site{}, fmt.Errorf("site directory %q is not absolute", f[1])
	}
	return Site{ID: f[0], Dir: filepath.Clean(f[1]), User: f[2], Port: port}, nil
}

// Sites returns the enabled sites. A missing registry is an empty one; a
// malformed line is an error, because skipping it would silently leave a site
// out of every deploy, backup and health check.
func (a *App) Sites() ([]Site, error) {
	data, err := os.ReadFile(a.registry())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sites []Site
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		site, err := parseSite(line)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", a.registry(), i+1, err)
		}
		sites = append(sites, site)
	}
	return sites, nil
}

// Pick returns the named sites, or every site when all is set.
func (a *App) Pick(ids []string, all bool) ([]Site, error) {
	sites, err := a.Sites()
	if err != nil {
		return nil, err
	}
	if all {
		if len(ids) > 0 {
			return nil, errors.New("name sites or use --all, not both")
		}
		return sites, nil
	}
	picked := make([]Site, 0, len(ids))
	for _, id := range ids {
		found := false
		for _, site := range sites {
			if site.ID == id {
				picked, found = append(picked, site), true
			}
		}
		if !found {
			return nil, fmt.Errorf("site %q is not in %s", id, a.registry())
		}
	}
	return picked, nil
}

// Site returns one site.
func (a *App) Site(id string) (Site, error) {
	sites, err := a.Pick([]string{id}, false)
	if err != nil {
		return Site{}, err
	}
	return sites[0], nil
}

// rewriteRegistry replaces the registry with its lines passed through keep,
// plus the lines in add.
func (a *App) rewriteRegistry(keep func(line string) bool, add ...string) error {
	data, err := os.ReadFile(a.registry())
	if errors.Is(err, os.ErrNotExist) {
		data = []byte(registryHeader)
	} else if err != nil {
		return err
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if keep(line) {
			out = append(out, line)
		}
	}
	out = append(out, add...)
	if err := os.MkdirAll(a.ConfDir, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(a.registry(), []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

func (a *App) register(site Site) error {
	return a.rewriteRegistry(func(string) bool { return true }, site.String())
}

func (a *App) unregister(id string) error {
	return a.rewriteRegistry(func(line string) bool {
		f := strings.Fields(line)
		return len(f) == 0 || f[0] != id
	})
}
