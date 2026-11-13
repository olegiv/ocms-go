// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package sitectl deploys and operates several instances ("sites") of one Go
// binary on a server, each a systemd unit <name>@<site>.
//
// The same code runs in two places. On the server it provisions sites,
// installs releases, restarts sites behind a health gate, backs them up and
// monitors them. On a development machine it builds, uploads and calls the
// server half over ssh. No shell is generated: every command is an argument
// list, and the server half is this package, not a script sent over the wire.
//
// The package knows nothing about the application it deploys; App carries
// everything specific to it. It must not import other packages of this module
// (TestSitectlIsApplicationAgnostic).
package sitectl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// App describes the deployed application and where it lives on a server.
type App struct {
	Name string // binary and unit name: unit <Name>@<site>, tool <Name>ctl

	Root          string // releases and tools, e.g. /opt/ocms
	ConfDir       string // site registry, alert hook, e.g. /etc/ocms
	SystemdDir    string // e.g. /etc/systemd/system
	CronFile      string // e.g. /etc/cron.d/ocms
	LogrotateFile string // e.g. /etc/logrotate.d/ocms
	StateDir      string // monitor state, cleared on reboot, e.g. /run/ocms
	SitesBase     string // parent of every site: <SitesBase>/<domain>/...

	DefaultGroup string // group of a new site's files
	BasePort     int    // first port handed to a new site
	HealthPath   string // answers 200 when the site is up
	ReadyPath    string // answers 200 when the site can serve
	DBPath       string // SQLite database, relative to the site directory
	ThemesPath   string // themes directory, relative to the site directory
	ThemeFile    string // file every theme directory holds, e.g. theme.json
	ErrorLog     string // the application's error-only log, relative to the site directory

	Unit           []byte // systemd template unit <Name>@.service
	EnvTemplate    string // a new site's .env (text/template)
	DropInTemplate string // a new site's systemd drop-in (text/template)
	ProxyTemplate  string // reverse-proxy snippet printed for a new site
	ScrubSQL       string // query returning the statements that scrub a pulled database

	Stdout io.Writer
	Stderr io.Writer
	Exec   func(ctx context.Context, c Cmd) error // runs external commands

	HealthTimeout time.Duration // how long a restarted site gets to answer
	Settle        time.Duration // pause before a healthy site is asked again
	Now           func() time.Time
}

// Cmd is one external command. It is never passed through a shell.
type Cmd struct {
	Name   string
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Cred   *syscall.Credential // run as another user (root only)
}

func (c Cmd) String() string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// RunCmd is the default App.Exec.
func RunCmd(ctx context.Context, c Cmd) error {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...) // #nosec G204 -- argument list, no shell
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	if c.Cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: c.Cred}
	}
	return cmd.Run()
}

// run executes a command with the app's output streams.
func (a *App) run(ctx context.Context, name string, args ...string) error {
	c := Cmd{Name: name, Args: args, Stdout: a.Stdout, Stderr: a.Stderr}
	if err := a.Exec(ctx, c); err != nil {
		return fmt.Errorf("%s: %w", c, err)
	}
	return nil
}

// output executes a command and returns its trimmed standard output, even
// when the command fails: `systemctl is-active` reports through both.
func (a *App) output(ctx context.Context, name string, args ...string) (string, error) {
	var out bytes.Buffer
	err := a.Exec(ctx, Cmd{Name: name, Args: args, Stdout: &out, Stderr: a.Stderr})
	return strings.TrimSpace(out.String()), err
}

func (a *App) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(a.Stdout, format+"\n", args...)
}

func (a *App) binDir() string      { return filepath.Join(a.Root, "bin") }
func (a *App) binary() string      { return filepath.Join(a.binDir(), a.Name) }
func (a *App) ctl() string         { return filepath.Join(a.binDir(), a.Name+"ctl") }
func (a *App) releasesDir() string { return filepath.Join(a.Root, "releases") }
func (a *App) registry() string    { return filepath.Join(a.ConfDir, "sites.conf") }
func (a *App) unit(id string) string {
	return a.Name + "@" + id
}

var (
	siteIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_]*$`)
	// safeArgRe is what may cross ssh. ssh hands the remote shell one string,
	// so an argument is sent only when the shell reads it as a single word.
	safeArgRe = regexp.MustCompile(`^[A-Za-z0-9._@:/=-]+$`)
)

func checkArgs(args ...string) error {
	for _, arg := range args {
		if !safeArgRe.MatchString(arg) {
			return fmt.Errorf("unsafe argument %q: letters, digits and ._@:/=- only", arg)
		}
	}
	return nil
}

// writeFileAtomic writes data beside path and renames it into place.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
