// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package sitectl deploys and operates several instances ("sites") of one Go
// binary on a server, each a systemd unit <name>@<site>.
//
// The same code runs in two places. On the server it provisions sites,
// installs releases, restarts sites behind a health gate, backs them up and
// monitors them. On a development machine it builds, uploads and calls the
// server half over ssh. Commands use argument lists; only initial bootstrap
// sends a fixed installer line before the server tool is available.
//
// The package knows nothing about the application it deploys; App carries
// everything specific to it. It must not import other packages of this module
// (TestSitectlIsApplicationAgnostic).
package sitectl

import (
	"bytes"
	"context"
	"errors"
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

	// What a site's .env, or a development checkout's configuration, may
	// set. Every path ocmsctl reads or writes for a site comes from these.
	DB         Setting // SQLite database
	Uploads    Setting // uploaded files: backed up, mirrored by pull
	Custom     Setting // the site's own files: backed up; holds ThemesPath
	ErrorLog   Setting // the application's error-only log; no Default: none
	Port       Setting // port a development checkout's server listens on
	EnvFileEnv string  // variable naming a checkout's env file instead of .env
	ThemesPath string  // themes directory, relative to Custom
	ThemeFile  string  // file every theme directory holds, e.g. theme.json
	LogsPath   string  // where the unit writes its output, relative to the site directory

	Unit           []byte // systemd template unit <Name>@.service
	EnvTemplate    string // a new site's .env (text/template)
	DropInTemplate string // a new site's systemd drop-in (text/template)
	ProxyTemplate  string // reverse-proxy snippet printed for a new site
	ScrubSQL       string // query returning the statements that scrub a pulled database

	Stdout    io.Writer
	Stderr    io.Writer
	Exec      func(ctx context.Context, c Cmd) error // runs external commands
	LookupEnv func(key string) (string, bool)        // the process environment (pull); nil: none

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
	Group  bool                // own process group, all of it ended with ctx
}

func (c Cmd) String() string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// RunCmd is the default App.Exec.
func RunCmd(ctx context.Context, c Cmd) error {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...) // #nosec G204 -- argument list, no shell
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: c.Cred, Setpgid: c.Group}
	if c.Group {
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = time.Second // for output pipes a stray child still holds
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

// output executes a command and returns its trimmed standard output.
func (a *App) output(ctx context.Context, name string, args ...string) (string, error) {
	var out bytes.Buffer
	err := a.Exec(ctx, Cmd{Name: name, Args: args, Stdout: &out, Stderr: a.Stderr})
	return strings.TrimSpace(out.String()), err
}

// tailBuffer keeps the last bytes written to it.
type tailBuffer struct{ data []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.data = append(t.data, p...)
	if extra := len(t.data) - 4096; extra > 0 {
		t.data = t.data[extra:]
	}
	return len(p), nil
}

// lastLine returns the last non-empty line written.
func (t *tailBuffer) lastLine() string {
	text := strings.TrimSpace(string(t.data))
	if i := strings.LastIndexByte(text, '\n'); i >= 0 {
		text = text[i+1:]
	}
	return strings.TrimSpace(text)
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

// createFile writes a new file and leaves one that exists alone.
func createFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return errors.Join(err, f.Chmod(mode), f.Close()) // Chmod: the umask narrowed mode
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
