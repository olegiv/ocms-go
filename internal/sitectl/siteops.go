// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

const backupRetention = 30 * 24 * time.Hour

// Site operations touch files the site user controls, so they never run as
// root: a directory swapped for a symlink must not gain root's reach. Root
// runs the installed tool again as the site user (SiteOpCommand); the site
// user runs the operation directly.
const SiteOpCommand = "_site"

// asSiteUser runs a site operation with the site user's rights.
func (a *App) asSiteUser(ctx context.Context, site Site, op string, stdin io.Reader, stdout io.Writer) error {
	u, err := user.Lookup(site.User)
	if err != nil {
		return err
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return err
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return err
	}
	if uint64(os.Geteuid()) == uid {
		return a.SiteOp(ctx, op, site.Dir, stdin, stdout)
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("run this as root or as %s", site.User)
	}
	// The installed tool, not os.Executable: the site user must be able to
	// run it, and a copy in root's home would not be. Group IDs are left
	// empty on purpose: the site user gets its primary group and none of
	// root's.
	cred := &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	return a.Exec(ctx, Cmd{Name: a.ctl(), Args: []string{SiteOpCommand, op, site.Dir}, Stdin: stdin, Stdout: stdout, Stderr: a.Stderr, Cred: cred})
}

// SiteOp runs one site operation in dir with the caller's own rights.
func (a *App) SiteOp(ctx context.Context, op, dir string, stdin io.Reader, stdout io.Writer) error {
	// Read site configuration with the site user's authority, including
	// when this operation was re-executed after dropping root privileges.
	if op == "backup" || op == "snapshot" {
		path, err := a.siteDBPath(dir)
		if err != nil {
			return err
		}
		copy := *a
		copy.DBPath = path
		a = &copy
	}
	switch op {
	case "backup":
		return a.backupSite(ctx, dir)
	case "snapshot":
		return a.snapshotSite(ctx, dir, stdout)
	case "themes":
		return a.installThemes(dir, stdin)
	default:
		return fmt.Errorf("unknown site operation %q", op)
	}
}

// Backup backs up the sites pick selects and reports every failure. Cron
// appends the run to a log nobody reads, so each failure, the selection's
// included (as site ServerSite), also goes to the alert hook as BACKUP
// FAILED; what the hook does not take waits for the next check.
func (a *App) Backup(ctx context.Context, pick func() ([]Site, error)) error {
	s := &sender{a: a}
	sites, err := pick()
	if err != nil {
		s.send(ctx, a.serverEvent("BACKUP FAILED", err.Error()))
	} else {
		err = a.each(sites, func(site Site) error {
			a.logf("Backing up %s", site.ID)
			err := a.asSiteUser(ctx, site, "backup", nil, a.Stdout)
			if err != nil {
				s.send(ctx, a.siteEvent(site, "BACKUP FAILED", err.Error()))
			}
			return err
		})
	}
	if len(s.pending) > 0 {
		if keepErr := a.keepAlerts(s.pending); keepErr != nil {
			a.logf("alert hook: %d alerts lost: %v", len(s.pending), keepErr)
		}
	}
	return err
}

var backupStampRe = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}$`)

// backupKind is one kind of archive backupSite writes: <prefix><stamp><suffix>.
type backupKind struct{ prefix, suffix string }

func (k backupKind) name(stamp string) string { return k.prefix + stamp + k.suffix }

// matches reports whether name is an archive of this kind. Retention removes
// nothing else: other files in backups/ are the operator's.
func (k backupKind) matches(name string) bool {
	stamp, ok := strings.CutPrefix(name, k.prefix)
	if !ok {
		return false
	}
	stamp, ok = strings.CutSuffix(stamp, k.suffix)
	return ok && backupStampRe.MatchString(stamp)
}

// backupSite writes a database snapshot and archives of uploads and custom
// content into <dir>/backups (owner-only). Only when all of them succeed does
// it drop archives older than backupRetention, of the kinds it just wrote.
// Anything it cannot read is an error, never "nothing to back up".
func (a *App) backupSite(ctx context.Context, dir string) error {
	// Creating backups/ below would otherwise recreate a missing site.
	if info, err := os.Stat(dir); err != nil {
		return fmt.Errorf("site directory: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("site directory %s is not a directory", dir)
	}
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(backups, 0o700); err != nil {
		return err
	}
	stamp := a.Now().Format("20060102_150405")
	var errs []error
	var written []backupKind

	dbKind := backupKind{a.Name + "_", ".db.gz"}
	if err := backupDB(ctx, a.databasePath(dir), filepath.Join(backups, dbKind.name(stamp))); err != nil {
		errs = append(errs, err)
	} else {
		written = append(written, dbKind)
	}
	for _, sub := range []string{"uploads", "custom"} {
		kind := backupKind{sub + "_", ".tar.gz"}
		root, err := contentRoot(filepath.Join(dir, sub))
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", sub, err))
		case root == "":
			a.logf("  %s: nothing to back up", sub)
		default:
			err := writeNew(filepath.Join(backups, kind.name(stamp)), func(w io.Writer) error {
				return writeTarGz(w, root, sub, false)
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", sub, err))
			} else {
				written = append(written, kind)
			}
		}
	}

	// Failed backups must leave the existing recovery copies intact.
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return a.expireBackups(backups, written)
}

// backupDB writes a gzipped, integrity-checked snapshot of the database db to
// the new file dst. A missing database is an error: a registered site without
// one is broken or names the wrong file in its configuration.
func backupDB(ctx context.Context, db, dst string) error {
	if _, err := os.Stat(db); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dst), ".snapshot-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	snapshot := filepath.Join(tmp, "snapshot.db")
	if err := snapshotDB(ctx, db, snapshot); err != nil {
		return err
	}
	if err := checkIntegrity(ctx, snapshot); err != nil {
		// Kept uncompressed for inspection; retention never matches it.
		return errors.Join(err, os.Rename(snapshot, strings.TrimSuffix(dst, ".gz")+".failed"))
	}
	return gzipFile(snapshot, dst)
}

// expireBackups removes this tool's archives older than backupRetention, of
// the given kinds.
func (a *App) expireBackups(backups string, kinds []backupKind) error {
	entries, err := os.ReadDir(backups)
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if !slices.ContainsFunc(kinds, func(k backupKind) bool { return k.matches(entry.Name()) }) {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if info.Mode().IsRegular() && a.Now().Sub(info.ModTime()) >= backupRetention {
			errs = append(errs, os.Remove(filepath.Join(backups, entry.Name())))
		}
	}
	return errors.Join(errs...)
}

// contentRoot returns the directory to archive for path, the target when path
// is a symlink, or "" when path does not exist or holds no file.
func contentRoot(path string) (string, error) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	// Backups run as the site user, so following the site's own link
	// reaches nothing that user cannot already read. A dangling link is
	// an error: its content is gone.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	found, err := hasFiles(resolved)
	if err != nil || !found {
		return "", err
	}
	return resolved, nil
}

// Snapshot writes a gzipped snapshot of the site's database to the app's
// standard output; `pull` reads it over ssh.
func (a *App) Snapshot(ctx context.Context, site Site) error {
	return a.asSiteUser(ctx, site, "snapshot", nil, a.Stdout)
}

// snapshotSite writes a gzipped, consistent copy of the site's database to
// w. The site keeps running: VACUUM INTO reads inside one transaction.
func (a *App) snapshotSite(ctx context.Context, dir string, w io.Writer) error {
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(backups, ".snapshot-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	snapshot := filepath.Join(tmp, "snapshot.db")
	if err := snapshotDB(ctx, a.databasePath(dir), snapshot); err != nil {
		return err
	}
	src, err := os.Open(snapshot) // #nosec G304 -- file this function just created
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	zw := gzip.NewWriter(w)
	if _, err := io.Copy(zw, src); err != nil {
		return err
	}
	return zw.Close()
}

func openDB(path, mode string) (*sql.DB, error) {
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("unsupported database path %q", path)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode="+mode+"&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // PRAGMAs are per connection
	return db, nil
}

// snapshotDB copies the SQLite database src into the new file dst (0600).
func snapshotDB(ctx context.Context, src, dst string) error {
	db, err := openDB(src, "ro")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("snapshot of %s: %w", src, err)
	}
	return os.Chmod(dst, 0o600)
}

func checkIntegrity(ctx context.Context, path string) error {
	db, err := openDB(path, "ro")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check of %s: %s", path, result)
	}
	return nil
}

// hasFiles reports whether dir holds at least one regular file. An entry it
// cannot read is an error: skipping it would back up nothing and succeed.
func hasFiles(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s is not a directory", dir)
	}
	found := false
	err = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found, err
}

// gzipFile writes src gzipped to the new file dst (0600).
func gzipFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- backup file this process created
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return writeNew(dst, func(w io.Writer) error {
		zw := gzip.NewWriter(w)
		_, err := io.Copy(zw, in)
		return errors.Join(err, zw.Close())
	})
}

// writeNew creates the file path (0600) holding what fill writes. The content
// goes to a temporary name beside path first, so path only ever appears
// complete; a failure leaves nothing behind, and an existing path is never
// replaced.
func writeNew(path string, fill func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".partial-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := errors.Join(fill(tmp), tmp.Close()); err != nil {
		return err
	}
	// A link, unlike a rename, fails when path already exists.
	return os.Link(tmp.Name(), path)
}

// writeTarGz writes the tree at root to w as a gzipped tar whose entries
// start with prefix. With follow set, symlinks are replaced by what they
// point to (a site repository may link its themes in); otherwise they are
// stored as links.
func writeTarGz(w io.Writer, root, prefix string, follow bool) error {
	var confined *os.Root
	var base string
	if follow {
		// A shared theme may live beside themes/, but a pulled symlink must
		// never export developer files or compiled module sources.
		// Abs first: it builds on $PWD, which may itself pass through a
		// symlink, and addToTar compares fully resolved paths against base.
		var err error
		base, err = filepath.Abs(filepath.Dir(filepath.Clean(root)))
		if err != nil {
			return err
		}
		base, err = filepath.EvalSymlinks(base)
		if err != nil {
			return err
		}
		confined, err = os.OpenRoot(base)
		if err != nil {
			return err
		}
		defer func() { _ = confined.Close() }()
		root = filepath.Join(base, filepath.Base(filepath.Clean(root)))
	}
	zw := gzip.NewWriter(w)
	tw := tar.NewWriter(zw)
	err := addToTar(tw, root, prefix, confined, base, 0)
	return errors.Join(err, tw.Close(), zw.Close())
}

func addToTar(tw *tar.Writer, path, name string, confined *os.Root, base string, depth int) error {
	if depth > 64 {
		return fmt.Errorf("%s: nested too deep (a symlink loop?)", path)
	}
	var file *os.File
	var info os.FileInfo
	var err error
	if confined != nil {
		// Canonicalization permits absolute links that stay in custom/.
		// The actual open below independently refuses changed symlinks.
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			return resolveErr
		}
		rel, relErr := filepath.Rel(base, resolved)
		first, _, _ := strings.Cut(rel, string(filepath.Separator))
		// Case aliases name the same directory on common macOS volumes.
		if relErr != nil || !filepath.IsLocal(rel) || strings.EqualFold(first, "modules") {
			return fmt.Errorf("theme path %q must stay inside %s and outside modules", path, base)
		}
		file, err = openArchiveFile(confined, rel)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		info, err = file.Stat()
	} else {
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	link := ""
	if info.Mode()&fs.ModeSymlink != 0 {
		if link, err = os.Readlink(path); err != nil {
			return err
		}
	} else if !info.IsDir() && !info.Mode().IsRegular() {
		return nil // sockets, devices, pipes
	}
	header, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(name)
	if info.IsDir() {
		header.Name += "/"
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	switch {
	case info.IsDir():
		var entries []os.DirEntry
		if file != nil {
			entries, err = file.ReadDir(-1)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		} else {
			entries, err = os.ReadDir(path)
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := addToTar(tw, filepath.Join(path, entry.Name()), filepath.Join(name, entry.Name()), confined, base, depth+1); err != nil {
				return err
			}
		}
	case info.Mode().IsRegular():
		if file == nil {
			file, err = os.Open(path) // #nosec G304 -- backup runs as the site user
			if err != nil {
				return err
			}
		}
		_, err = io.Copy(tw, file)
		return errors.Join(err, file.Close())
	}
	return nil
}

// openArchiveFile opens a canonical relative name without following another
// symlink at any component. Each descriptor is checked against its no-follow
// metadata, so a replacement cannot redirect reads after the policy check.
func openArchiveFile(root *os.Root, path string) (*os.File, error) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts[:len(parts)-1] {
		info, err := root.Lstat(part)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("archive directory %q changed", part)
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			return nil, err
		}
		defer func() { _ = next.Close() }()
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			return nil, fmt.Errorf("archive directory %q changed while opening", part)
		}
		root = next
	}
	leaf := parts[len(parts)-1]
	info, err := root.Lstat(leaf)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("archive path %q changed to a symlink", path)
	}
	file, err := root.OpenFile(leaf, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("archive path %q changed while opening", path)
	}
	return file, nil
}

// InstallThemes replaces a site's themes with the gzipped tar read from r.
func (a *App) InstallThemes(ctx context.Context, site Site, r io.Reader) error {
	if err := a.asSiteUser(ctx, site, "themes", r, a.Stdout); err != nil {
		return err
	}
	a.logf("Themes of %s replaced", site.ID)
	return nil
}

// installThemes unpacks the archive beside the themes directory and swaps
// the two, so a broken upload leaves the old themes in place.
func (a *App) installThemes(dir string, r io.Reader) error {
	themes := filepath.Join(dir, a.ThemesPath)
	parent := filepath.Dir(themes)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	fresh, err := os.MkdirTemp(parent, ".themes-new-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(fresh) }()
	if err := extractTarGz(r, fresh); err != nil {
		return fmt.Errorf("unpacking themes: %w", err)
	}
	if err := os.Chmod(fresh, 0o755); err != nil {
		return err
	}
	old := fresh + ".old"
	if err := os.Rename(themes, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(fresh, themes); err != nil {
		return errors.Join(err, os.Rename(old, themes))
	}
	return os.RemoveAll(old)
}

// extractTarGz unpacks directories and regular files into dir. Anything
// else, and any name that would leave dir, is an error.
func extractTarGz(r io.Reader, dir string) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(header.Name) {
			return fmt.Errorf("unsafe path %q in archive", header.Name)
		}
		target := filepath.Join(dir, header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // #nosec G304 -- IsLocal name under a fresh directory
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr) // #nosec G110 -- the operator's own upload
			if err = errors.Join(err, f.Close()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported entry %q in archive", header.Name)
		}
	}
}

// siteDBPath reads only the configured key; it never executes the site's .env.
func (a *App) siteDBPath(dir string) (string, error) {
	if a.DBPathEnv == "" {
		return a.DBPath, nil
	}
	values, err := godotenv.Read(filepath.Join(dir, ".env"))
	if errors.Is(err, os.ErrNotExist) {
		return a.DBPath, nil
	}
	if err != nil {
		return "", fmt.Errorf("site configuration: %w", err)
	}
	if path := values[a.DBPathEnv]; path != "" {
		return path, nil
	}
	return a.DBPath, nil
}

func (a *App) databasePath(dir string) string {
	if filepath.IsAbs(a.DBPath) {
		return a.DBPath
	}
	return filepath.Join(dir, a.DBPath)
}
