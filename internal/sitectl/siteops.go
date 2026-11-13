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
	"strconv"
	"strings"
	"syscall"
	"time"

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

// Backup backs up the sites and reports every failure.
func (a *App) Backup(ctx context.Context, sites []Site) error {
	return a.each(sites, func(site Site) error {
		a.logf("Backing up %s", site.ID)
		return a.asSiteUser(ctx, site, "backup", nil, a.Stdout)
	})
}

// backupSite writes a database snapshot and archives of uploads and custom
// content into <dir>/backups (owner-only), then drops backups older than
// backupRetention.
func (a *App) backupSite(ctx context.Context, dir string) error {
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(backups, 0o700); err != nil {
		return err
	}
	stamp := a.Now().Format("20060102_150405")
	var errs []error

	if db := filepath.Join(dir, a.DBPath); exists(db) {
		snapshot := filepath.Join(backups, a.Name+"_"+stamp+".db")
		err := snapshotDB(ctx, db, snapshot)
		if err == nil {
			// A snapshot that fails the check is kept, but the run fails.
			errs = append(errs, checkIntegrity(ctx, snapshot))
			err = gzipFile(snapshot)
		}
		errs = append(errs, err)
	} else {
		a.logf("  no database at %s", db)
	}
	for _, sub := range []string{"uploads", "custom"} {
		if hasFiles(filepath.Join(dir, sub)) {
			errs = append(errs, tarGzFile(filepath.Join(backups, sub+"_"+stamp+".tar.gz"), dir, sub))
		}
	}

	entries, err := os.ReadDir(backups)
	errs = append(errs, err)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || a.Now().Sub(info.ModTime()) < backupRetention {
			continue
		}
		if name := entry.Name(); strings.HasSuffix(name, ".db.gz") || strings.HasSuffix(name, ".tar.gz") {
			errs = append(errs, os.Remove(filepath.Join(backups, name)))
		}
	}
	return errors.Join(errs...)
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
	if err := snapshotDB(ctx, filepath.Join(dir, a.DBPath), snapshot); err != nil {
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

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// hasFiles reports whether dir holds at least one regular file.
func hasFiles(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// gzipFile replaces path with path.gz (0600).
func gzipFile(path string) error {
	src, err := os.Open(path) // #nosec G304 -- backup file this process created
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(path+".gz", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(dst)
	_, err = io.Copy(zw, src)
	err = errors.Join(err, zw.Close(), dst.Close())
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// tarGzFile archives <base>/<sub> into the new file dst (0600).
func tarGzFile(dst, base, sub string) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- inside the site's backups directory
	if err != nil {
		return err
	}
	err = writeTarGz(out, filepath.Join(base, sub), sub, false)
	return errors.Join(err, out.Close())
}

// writeTarGz writes the tree at root to w as a gzipped tar whose entries
// start with prefix. With follow set, symlinks are replaced by what they
// point to (a site repository may link its themes in); otherwise they are
// stored as links.
func writeTarGz(w io.Writer, root, prefix string, follow bool) error {
	zw := gzip.NewWriter(w)
	tw := tar.NewWriter(zw)
	err := addToTar(tw, root, prefix, follow, 0)
	return errors.Join(err, tw.Close(), zw.Close())
}

func addToTar(tw *tar.Writer, path, name string, follow bool, depth int) error {
	if depth > 64 {
		return fmt.Errorf("%s: nested too deep (a symlink loop?)", path)
	}
	info, err := os.Lstat(path)
	if err == nil && follow && info.Mode()&fs.ModeSymlink != 0 {
		info, err = os.Stat(path)
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
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := addToTar(tw, filepath.Join(path, entry.Name()), filepath.Join(name, entry.Name()), follow, depth+1); err != nil {
				return err
			}
		}
	case info.Mode().IsRegular():
		f, err := os.Open(path) // #nosec G304 -- walking a tree the caller named
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return errors.Join(err, f.Close())
	}
	return nil
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
