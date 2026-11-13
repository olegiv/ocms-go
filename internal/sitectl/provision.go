// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"text/template"
)

var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// SiteSpec is what `site add` takes.
type SiteSpec struct {
	Domain string
	User   string
	Group  string // default App.DefaultGroup
	Port   int    // 0 picks the first free port from App.BasePort
	Dir    string // default <SitesBase>/<domain>/<Name>
}

// siteVars is what the site templates see. Secret and HexSecret return a new
// random value on every call.
type siteVars struct {
	Site
	Domain string
	Group  string
	Unit   string
	Date   string
}

func (siteVars) Secret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func (siteVars) HexSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func render(name, text string, vars siteVars) ([]byte, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, vars); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// lookupIDs resolves a user and a group name to numeric ids.
func lookupIDs(userName, groupName string) (uid, gid int, err error) {
	u, err := user.Lookup(userName)
	if err != nil {
		return 0, 0, err
	}
	g, err := user.LookupGroup(groupName)
	if err != nil {
		return 0, 0, err
	}
	if uid, err = strconv.Atoi(u.Uid); err != nil {
		return 0, 0, err
	}
	gid, err = strconv.Atoi(g.Gid)
	return uid, gid, err
}

// AddSite provisions a site: its directories, .env with generated secrets,
// systemd drop-in and registry entry. It starts nothing.
func (a *App) AddSite(ctx context.Context, spec SiteSpec) error {
	if !domainRe.MatchString(spec.Domain) {
		return fmt.Errorf("invalid domain %q", spec.Domain)
	}
	if spec.Group == "" {
		spec.Group = a.DefaultGroup
	}
	uid, gid, err := lookupIDs(spec.User, spec.Group)
	if err != nil {
		return err
	}
	base := filepath.Join(a.SitesBase, spec.Domain)
	dir := filepath.Join(base, a.Name)
	if spec.Dir != "" {
		dir = filepath.Clean(spec.Dir)
	}
	if !strings.HasPrefix(dir, base+string(filepath.Separator)) {
		return fmt.Errorf("site directory must be under %s", base)
	}
	if _, err := os.Stat(filepath.Dir(dir)); err != nil {
		return fmt.Errorf("parent directory: %w", err)
	}
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("%s already exists; remove it to provision the site again", dir)
	}

	sites, err := a.Sites()
	if err != nil {
		return err
	}
	site := Site{ID: strings.NewReplacer(".", "_", "-", "_").Replace(spec.Domain), Dir: dir, User: spec.User, Port: spec.Port}
	used := map[int]bool{}
	for _, other := range sites {
		if other.ID == site.ID {
			return fmt.Errorf("site %s is already registered", site.ID)
		}
		used[other.Port] = true
	}
	if site.Port == 0 {
		for site.Port = a.BasePort; used[site.Port]; site.Port++ {
		}
	} else if used[site.Port] {
		return fmt.Errorf("port %d belongs to another site", site.Port)
	}

	vars := siteVars{Site: site, Domain: spec.Domain, Group: spec.Group, Unit: a.unit(site.ID), Date: a.Now().UTC().Format("2006-01-02T15:04:05Z")}
	env, err := render("env", a.EnvTemplate, vars)
	if err != nil {
		return err
	}
	dropIn, err := render("drop-in", a.DropInTemplate, vars)
	if err != nil {
		return err
	}
	proxy, err := render("proxy", a.ProxyTemplate, vars)
	if err != nil {
		return err
	}

	// The directory stays private to root until everything in it is owned
	// by the site user.
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	for sub, mode := range map[string]os.FileMode{"data": 0o755, "uploads": 0o755, a.ThemesPath: 0o755, "logs": 0o755, "backups": 0o700} {
		if err := os.MkdirAll(filepath.Join(dir, sub), mode); err != nil {
			return err
		}
	}
	if err := writeFileAtomic(filepath.Join(dir, ".env"), env, 0o600); err != nil {
		return err
	}
	err = filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
	if err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		return err
	}

	dropInDir := filepath.Join(a.SystemdDir, a.unit(site.ID)+".service.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dropInDir, "instance.conf"), dropIn, 0o644); err != nil {
		return err
	}
	if err := a.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := a.register(site); err != nil {
		return err
	}
	if err := a.writeLogrotate(); err != nil {
		return err
	}

	a.logf("Site %s provisioned: %s, user %s:%s, port %d, unit %s", site.ID, dir, spec.User, spec.Group, site.Port, a.unit(site.ID))
	a.logf("Review %s, then: systemctl enable --now %s\n", filepath.Join(dir, ".env"), a.unit(site.ID))
	_, _ = a.Stdout.Write(proxy)
	return nil
}

// RemoveSite stops and unregisters a site. Its directory is left alone.
func (a *App) RemoveSite(ctx context.Context, site Site) error {
	if err := a.systemctl(ctx, "disable", "--now", a.unit(site.ID)); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(a.SystemdDir, a.unit(site.ID)+".service.d")); err != nil {
		return err
	}
	if err := a.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := a.unregister(site.ID); err != nil {
		return err
	}
	if err := a.writeLogrotate(); err != nil {
		return err
	}
	a.logf("Site %s removed from systemd and the registry. Its data is still in %s", site.ID, site.Dir)
	return nil
}

// Init prepares a server: directories, the systemd template unit, cron jobs
// for backups and monitoring, and log rotation. It is safe to run again; it
// does not replace an existing cron file.
func (a *App) Init(ctx context.Context) error {
	for _, dir := range []string{a.binDir(), a.releasesDir(), a.ConfDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := writeFileAtomic(filepath.Join(a.SystemdDir, a.Name+"@.service"), a.Unit, 0o644); err != nil {
		return err
	}
	if err := a.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if _, err := os.Stat(a.CronFile); errors.Is(err, os.ErrNotExist) {
		cron := fmt.Sprintf("# Nightly backups and a health check every five minutes.\n"+
			"# The check prints only when a site's state changes; set MAILTO to be told.\n"+
			"SHELL=/bin/sh\n"+
			"0 3 * * * root %[1]s backup --all >> /var/log/%[2]s-backup.log 2>&1\n"+
			"*/5 * * * * root %[1]s check --all\n", a.ctl(), a.Name)
		if err := writeFileAtomic(a.CronFile, []byte(cron), 0o644); err != nil {
			return err
		}
	}
	if err := a.writeLogrotate(); err != nil {
		return err
	}
	a.logf("Server ready: %s, unit %s@.service, %s, %s", a.Root, a.Name, a.CronFile, a.LogrotateFile)
	return nil
}

// writeLogrotate regenerates the logrotate configuration from the registry.
// copytruncate, because systemd keeps the log file open across a rotation.
func (a *App) writeLogrotate() error {
	sites, err := a.Sites()
	if err != nil {
		return err
	}
	var out strings.Builder
	out.WriteString("# Generated from " + a.registry() + "; do not edit.\n")
	const options = "    rotate 30\n    compress\n    delaycompress\n    missingok\n    notifempty\n"
	for _, site := range sites {
		fmt.Fprintf(&out, "\n%s/logs/*.log {\n    su %s %s\n    daily\n%s    copytruncate\n}\n", site.Dir, site.User, a.groupOf(site.Dir), options)
	}
	fmt.Fprintf(&out, "\n/var/log/%s-backup.log {\n    weekly\n%s}\n", a.Name, options)
	return writeFileAtomic(a.LogrotateFile, []byte(out.String()), 0o644)
}

// groupOf returns the name of the group that owns path.
func (a *App) groupOf(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return a.DefaultGroup
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return a.DefaultGroup
	}
	group, err := user.LookupGroupId(strconv.FormatUint(uint64(stat.Gid), 10))
	if err != nil {
		return a.DefaultGroup
	}
	return group.Name
}
