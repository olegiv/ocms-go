// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// This file is the development-machine half: it builds and uploads, then
// calls the server half (`<Name>ctl ...`) over ssh. The system ssh and rsync
// are used, so ~/.ssh/config, agents and jump hosts apply.

// remote is one call of the server's ctl over ssh.
type remote struct {
	app    *App
	host   string
	dryRun bool
}

func (a *App) remote(host string, dryRun bool) (*remote, error) {
	// A leading dash would make ssh read the host as one of its options.
	if err := checkArgs(host); err != nil || strings.HasPrefix(host, "-") {
		return nil, fmt.Errorf("invalid host %q: an ssh destination such as root@server", host)
	}
	return &remote{app: a, host: host, dryRun: dryRun}, nil
}

// ctl runs `<Name>ctl args...` on the server. stdin, when set, is the file
// streamed to it; stdout defaults to the app's.
func (r *remote) ctl(ctx context.Context, stdinFile string, stdout io.Writer, args ...string) error {
	if err := checkArgs(args...); err != nil {
		return err
	}
	return r.exec(ctx, stdinFile, stdout, "ssh", append([]string{"-C", r.host, r.app.ctl()}, args...)...)
}

func (r *remote) exec(ctx context.Context, stdinFile string, stdout io.Writer, name string, args ...string) error {
	c := Cmd{Name: name, Args: args, Stdout: stdout, Stderr: r.app.Stderr}
	if c.Stdout == nil {
		c.Stdout = r.app.Stdout
	}
	if r.dryRun {
		line := "[dry-run] " + c.String()
		if stdinFile != "" {
			line += " < " + stdinFile
		}
		r.app.logf("%s", line)
		return nil
	}
	if stdinFile != "" {
		f, err := os.Open(stdinFile) // #nosec G304 -- file the operator named
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		c.Stdin = f
	}
	if err := r.app.Exec(ctx, c); err != nil {
		return fmt.Errorf("%s: %w", c, err)
	}
	return nil
}

// DeployOpts is what `deploy` takes.
type DeployOpts struct {
	Host       string   // ssh destination, e.g. root@server or an ssh config alias
	Sites      []string // sites to restart on the new binary
	All        bool     // restart every registered site
	Binary     string   // Linux binary to upload
	Build      []string // command that builds Binary; empty skips the build
	Themes     bool     // also replace the named sites' themes with ThemesDir
	ThemesOnly bool     // replace themes and restart, leave the binary alone
	ThemesDir  string   // empty: the checkout's own, from its configuration
	DryRun     bool

	HealthTimeout time.Duration // passed to the server when set
}

// Deploy builds the binary, uploads it and has the server install it and
// restart the sites behind the health gate. The upload is verified against
// the SHA-256 computed here before the server touches anything.
func (a *App) Deploy(ctx context.Context, o DeployOpts) error {
	r, err := a.remote(o.Host, o.DryRun)
	if err != nil {
		return err
	}
	if len(o.Sites) == 0 && !o.All {
		return errors.New("name at least one site, or use --all")
	}
	targets := o.Sites
	if o.All {
		if len(o.Sites) > 0 {
			return errors.New("name sites or use --all, not both")
		}
		targets = []string{"--all"}
	}
	if o.HealthTimeout > 0 {
		targets = append([]string{"--health-timeout=" + o.HealthTimeout.String()}, targets...)
	}
	themes := o.Themes || o.ThemesOnly
	if themes && (o.All || len(o.Sites) == 0) {
		return errors.New("name the sites whose themes to replace")
	}
	if themes && o.ThemesDir == "" {
		local, err := a.readLocalConfig(".")
		if err != nil {
			return err
		}
		o.ThemesDir = local.themes(a)
	}
	// The upload replaces the server's themes, so placeholders alone
	// (.gitkeep, a README) must not count as a set of themes.
	if found, _ := filepath.Glob(filepath.Join(o.ThemesDir, "*", a.ThemeFile)); themes && len(found) == 0 {
		return fmt.Errorf("no theme (a directory with %s) in %s", a.ThemeFile, o.ThemesDir)
	}

	var archive string
	if themes {
		// Packed before anything is sent: a theme that cannot be packed
		// stops the deploy with the server untouched.
		f, err := os.CreateTemp("", a.Name+"-themes-*.tar.gz")
		if err != nil {
			return err
		}
		archive = f.Name()
		defer func() { _ = os.Remove(archive) }()
		err = writeTarGz(f, o.ThemesDir, ".", true)
		if err = errors.Join(err, f.Close()); err != nil {
			return fmt.Errorf("packing %s: %w", o.ThemesDir, err)
		}
	}
	sendThemes := func() error {
		for _, site := range o.Sites {
			if err := r.ctl(ctx, archive, nil, "themes", site); err != nil {
				return err
			}
		}
		return nil
	}
	if o.ThemesOnly {
		if err := sendThemes(); err != nil {
			return err
		}
		return r.ctl(ctx, "", nil, append([]string{"restart"}, targets...)...)
	}

	if len(o.Build) > 0 {
		if err := r.exec(ctx, "", nil, o.Build[0], o.Build[1:]...); err != nil {
			return err
		}
	}
	sum, err := fileSHA256(o.Binary)
	if err != nil {
		if !o.DryRun {
			return fmt.Errorf("binary: %w", err)
		}
		sum = strings.Repeat("0", 64) // dry run before the first build
	}
	if !themes {
		return r.ctl(ctx, o.Binary, nil, append([]string{"install", "--sha256", sum}, targets...)...)
	}
	// The server verifies and keeps the binary before the themes change, so
	// an upload that does not arrive intact changes nothing; then it
	// switches to that binary and restarts the sites once.
	if err := r.ctl(ctx, o.Binary, nil, "install", "--sha256", sum, "--stage"); err != nil {
		return err
	}
	if err := sendThemes(); err != nil {
		return err
	}
	return r.ctl(ctx, "", nil, append([]string{"install", "--sha256", sum, "--staged"}, targets...)...)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- file the operator named
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Bootstrap installs this tool on a server and prepares the server (Init).
// It is the one place a shell line is sent, because the tool is not there
// yet; the line is fixed and carries nothing but the tool's own path.
func (a *App) Bootstrap(ctx context.Context, host, ctlBinary string, dryRun bool) error {
	r, err := a.remote(host, dryRun)
	if err != nil {
		return err
	}
	if err := checkArgs(a.ctl()); err != nil {
		return err
	}
	if head, err := readHead(ctlBinary, 4); !dryRun && (err != nil || !bytes.Equal(head, []byte("\x7fELF"))) {
		return fmt.Errorf("%s is not a Linux executable (build it with GOOS=linux)", ctlBinary)
	}
	script := fmt.Sprintf("set -e; mkdir -p %[1]s; cat > %[2]s.new; chmod 755 %[2]s.new; mv -f %[2]s.new %[2]s", a.binDir(), a.ctl())
	if err := r.exec(ctx, ctlBinary, nil, "ssh", "-C", host, script); err != nil {
		return err
	}
	return r.ctl(ctx, "", nil, "init")
}

// PullOpts is what `pull` takes. Where the copies go comes from the
// configuration of the checkout in the working directory, as its own server
// reads it (see readLocalConfig); LocalDB and LocalPort override it.
type PullOpts struct {
	Host        string
	Site        string
	DB          bool   // replace the local database with a scrubbed snapshot
	Uploads     bool   // mirror the uploads (deletes local files the server lacks)
	Logs        bool   // copy the unit's log directory
	Themes      bool   // copy themes, keeping local-only files
	LocalDB     string // database to replace; empty: the checkout's
	LocalPort   int    // refuse to run while something listens here; 0: the checkout's port
	NoPortCheck bool
	Yes         bool // do not ask
	DryRun      bool
	Confirm     io.Reader // where the answer to the question comes from
}

// Pull copies a production site into the working directory for development.
// Production keeps running: the database arrives as an online snapshot, and
// it is scrubbed (App.ScrubSQL) before it replaces the local one. The result
// is still production data: treat it as such.
//
// Files arrive as directories and regular files only. A symlink the server
// holds is skipped, and the uploads mirror drops the ones an older pull left,
// so nothing the server controls can point a development server outside
// its own files. On the server, rsync reads as the site user (Send), so a
// site's configuration cannot make it copy another site's files or root's.
func (a *App) Pull(ctx context.Context, o PullOpts) error {
	r, err := a.remote(o.Host, o.DryRun)
	if err != nil {
		return err
	}
	if !siteIDRe.MatchString(o.Site) {
		return fmt.Errorf("invalid site %q", o.Site)
	}
	if err := checkArgs(a.ctl()); err != nil { // the remote shell reads it (--rsync-path)
		return err
	}
	if o.LocalPort < 0 || o.LocalPort > 65535 {
		return fmt.Errorf("invalid port %d", o.LocalPort)
	}
	local, err := a.readLocalConfig(".")
	if err != nil {
		return err
	}
	dbFrom, portFrom := "--db", "--port"
	if o.LocalDB == "" {
		o.LocalDB, dbFrom = local.DB, local.Sources[a.DB.Name]
	}
	if o.LocalDB, err = filepath.Abs(o.LocalDB); err != nil {
		return err
	}
	if o.LocalPort == 0 {
		o.LocalPort, portFrom = local.Port, local.Sources[a.Port.Name]
	}
	if o.Uploads {
		if err := mirrorTarget(local.Uploads, local.Dir, o.LocalDB, local.Custom); err != nil {
			return err
		}
	}
	if o.Themes && local.Custom == "" {
		return errors.New("the local configuration names no custom directory for themes")
	}
	// Said before anything happens, also with --yes: these come from the
	// environment and files, which may not say what the caller expects.
	a.logf("Pulling %s from %s into:", o.Site, o.Host)
	if o.DB {
		a.logf("  database  %s (%s)", o.LocalDB, dbFrom)
		if !o.NoPortCheck && o.LocalPort != 0 {
			a.logf("  port      %d must be free (%s)", o.LocalPort, portFrom)
		}
	}
	if o.Uploads {
		a.logf("  uploads   %s (%s), mirrored: what production lacks is deleted", local.Uploads, local.Sources[a.Uploads.Name])
	}
	if o.Logs {
		a.logf("  logs      %s", filepath.Join(local.Dir, a.LogsPath))
	}
	if o.Themes {
		a.logf("  themes    %s (%s)", local.themes(a), local.Sources[a.Custom.Name])
	}
	if !o.Yes && !o.DryRun {
		_, _ = fmt.Fprintf(a.Stdout, "This overwrites local data with production data from %s. Continue? [y/N] ", o.Site)
		answer, _ := bufio.NewReader(o.Confirm).ReadString('\n')
		if reply := strings.ToLower(strings.TrimSpace(answer)); reply != "y" && reply != "yes" {
			return errors.New("aborted")
		}
	}
	if o.DB && !o.NoPortCheck && o.LocalPort != 0 {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(o.LocalPort)), time.Second)
		if err == nil {
			_ = conn.Close()
			return fmt.Errorf("something is listening on port %d: stop the local server first (--port 0 skips this check)", o.LocalPort)
		}
	}

	var line bytes.Buffer
	if err := r.ctl(ctx, "", &line, "site", "show", o.Site); err != nil {
		return err
	}
	site := Site{ID: o.Site, Dir: "<site-dir>"}
	if !o.DryRun {
		if site, err = parseSite(line.String()); err != nil {
			return fmt.Errorf("server answered %q: %w", line.String(), err)
		}
		if err := checkArgs(site.Dir); err != nil {
			return err
		}
	}
	served := sitePaths{Uploads: "<uploads-dir>", Custom: "<custom-dir>"} // the server's paths
	if o.Uploads || o.Themes {
		var out bytes.Buffer
		var said tailBuffer // what the server prints about a failure
		app := *a
		app.Stderr = io.MultiWriter(a.Stderr, &said)
		asked := remote{app: &app, host: r.host, dryRun: r.dryRun}
		if err := asked.ctl(ctx, "", &out, "site", "paths", o.Site); err != nil {
			// An older ocmsctl answers with a usage line that lacks it.
			if usage := said.lastLine(); strings.Contains(usage, "usage:") && !strings.Contains(usage, "site paths") {
				return fmt.Errorf("%w: the server's ocmsctl predates `site paths`; update it (make server-setup)", err)
			}
			return err
		}
		if !o.DryRun {
			if served, err = a.parsePaths(out.String()); err != nil {
				return err
			}
		}
	}

	if o.DB {
		if err := a.pullDB(ctx, r, o); err != nil {
			return err
		}
	}
	rsync := func(from, to string, mirror bool) error {
		if !o.DryRun {
			if err := os.MkdirAll(to, 0o755); err != nil {
				return err
			}
		}
		// The server side reads as the site user (Send).
		args := append(pullFlags(mirror), "--rsync-path="+a.ctl()+" "+SendCommand+" "+o.Site, o.Host+":"+from+"/", to+"/")
		return r.exec(ctx, "", nil, "rsync", args...)
	}
	if o.Uploads {
		if err := a.dropSpecialFiles(local.Uploads, o.DryRun); err != nil {
			return err
		}
		if err := rsync(served.Uploads, local.Uploads, true); err != nil {
			return err
		}
	}
	if o.Logs {
		if err := rsync(site.Dir+"/"+filepath.ToSlash(a.LogsPath), filepath.Join(local.Dir, a.LogsPath), false); err != nil {
			return err
		}
	}
	if o.Themes {
		if err := rsync(served.Custom+"/"+filepath.ToSlash(a.ThemesPath), local.themes(a), false); err != nil {
			return err
		}
	}
	if !o.DryRun {
		a.logf("Pulled %s from %s", o.Site, o.Host)
	}
	return nil
}

// pullFlags copies directories and regular files with their times: no
// symlinks, devices or special files, and no owners or modes from the
// server. A mirror also compares content, as a local file can differ from
// the server's with the same size and time, and deletes what the server
// lacks.
func pullFlags(mirror bool) []string {
	flags := []string{"-rtz"}
	if mirror {
		flags = append(flags, "--checksum", "--delete")
	}
	return flags
}

// mirrorTarget refuses an uploads directory that the mirror's --delete
// would turn against the checkout: the checkout itself or a directory above
// it, or one that holds the database or the custom directory, also when a
// symlink leads there. It must be configured inside the checkout: a value
// left in the environment by another project would otherwise aim the
// deletions there. A link the developer made inside is followed.
func mirrorTarget(uploads, checkout string, keep ...string) error {
	if uploads == "" {
		return errors.New("the local configuration names no uploads directory")
	}
	for _, kept := range append([]string{checkout}, keep...) {
		if within(resolved(kept), resolved(uploads)) {
			return fmt.Errorf("the uploads directory %s holds %s, which mirroring production's uploads would delete", uploads, kept)
		}
	}
	if !within(uploads, checkout) {
		return fmt.Errorf("the uploads directory %s is outside this checkout (%s): pull mirrors uploads, deleting what production lacks, only inside it (--no-uploads skips them)", uploads, checkout)
	}
	return nil
}

// dropSpecialFiles removes what is neither a directory nor a regular file
// below dir (a link the developer made to dir itself is followed). rsync
// leaves such entries alone even with --delete; the server sends none.
func (a *App) dropSpecialFiles(dir string, dryRun bool) error {
	root, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Type().IsRegular() {
			return err
		}
		if dryRun {
			a.logf("[dry-run] remove %s", name)
			return nil
		}
		a.logf("Removing %s: the server sends directories and regular files only", name)
		return os.Remove(name)
	})
}

// pullDB downloads a snapshot beside the local database, scrubs it and only
// then moves it into place.
func (a *App) pullDB(ctx context.Context, r *remote, o PullOpts) error {
	if o.DryRun {
		if err := r.ctl(ctx, "", nil, "snapshot", o.Site); err != nil {
			return err
		}
		a.logf("[dry-run] scrub the snapshot, then replace %s", o.LocalDB)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(o.LocalDB), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(o.LocalDB), ".pull-*") // 0700
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	packed, err := os.Create(filepath.Join(tmp, "snapshot.db.gz")) // #nosec G304 -- inside the private directory above
	if err != nil {
		return err
	}
	err = r.ctl(ctx, "", packed, "snapshot", o.Site)
	if err = errors.Join(err, packed.Close()); err != nil {
		return err
	}
	snapshot := filepath.Join(tmp, "snapshot.db")
	if err := gunzipFile(packed.Name(), snapshot); err != nil {
		return fmt.Errorf("snapshot from the server: %w", err)
	}
	if err := a.Scrub(ctx, snapshot); err != nil {
		return fmt.Errorf("scrubbing the snapshot (nothing was replaced): %w", err)
	}
	for _, sidecar := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(o.LocalDB + sidecar); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(snapshot, o.LocalDB)
}

func gunzipFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- file this process downloaded
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304
	if err != nil {
		return err
	}
	_, err = io.Copy(out, zr) // #nosec G110 -- the operator's own database
	return errors.Join(err, out.Close())
}

// Scrub removes credentials and outbound integrations from a pulled
// database. App.ScrubSQL is a query that returns the statements to run for
// the tables this database has; they run in one transaction, and the file
// is then rewritten so the deleted values are gone from its free pages.
func (a *App) Scrub(ctx context.Context, path string) error {
	db, err := openDB(path, "rw")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, a.ScrubSQL)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var statements []string
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			return err
		}
		statements = append(statements, statement)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = DELETE; PRAGMA secure_delete = ON"); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("%s: %w", statement, err), tx.Rollback())
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "VACUUM")
	return err
}
