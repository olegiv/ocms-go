// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Command ocmsctl deploys and operates oCMS sites. The same binary runs on a
// development machine (bootstrap, deploy, pull) and on the server (everything
// else); internal/sitectl holds the logic, this file the oCMS specifics.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/olegiv/ocms-go/internal/sitectl"
)

var (
	//go:embed templates/ocms@.service
	unitTemplate []byte
	//go:embed templates/sync-db-policy.sql
	scrubSQL string
	//go:embed templates/env.tmpl
	envTemplate string
	//go:embed templates/dropin.tmpl
	dropInTemplate string
	//go:embed templates/nginx.tmpl
	proxyTemplate string
)

const usage = `ocmsctl deploys and operates oCMS sites.

On your machine (host is an ssh destination: root@server or an ssh alias):
  ocmsctl bootstrap <host>                 install ocmsctl on a server and prepare it
  ocmsctl deploy <host> <site>...|--all    build, upload, install, restart, health-check
        [--themes | --themes-only] [--no-build] [--binary FILE] [--arch amd64|arm64]
  ocmsctl pull <host> <site>               copy a scrubbed production snapshot here
        [--no-db] [--no-uploads] [--no-logs] [--themes] [--yes] [--db FILE] [--port N]
        (paths and port as this checkout's server reads them: environment, .env)
  Add --dry-run to print the commands instead of running them.

On the server, as root:
  ocmsctl site add <domain> <user> [--port N] [--group G] [--dir PATH]
  ocmsctl site remove <site>               unregister; the site's data stays
  ocmsctl status [site...]                 unit state and health (all sites by default);
                                           exits 1 while a site is down
  ocmsctl start|stop <site>...|--all
  ocmsctl restart <site>...|--all          stop, start, require healthy
  ocmsctl health <site>
  ocmsctl logs <site> [-f] [--errors]
  ocmsctl releases                         installed releases, * marks the current one
  ocmsctl rollback <site>...|--all [--to RELEASE]
  ocmsctl backup [site...]                 all sites by default
  ocmsctl check [site...]                  monitor for cron; restarts unhealthy sites

Used by deploy and pull over ssh: init, install, themes, snapshot, site show,
site paths, _send.
`

// newApp returns the oCMS deployment: a Plesk-style server with one binary in
// /opt/ocms shared by every site under /var/www/vhosts.
func newApp() *sitectl.App {
	return &sitectl.App{
		Name:          "ocms",
		Root:          "/opt/ocms",
		ConfDir:       "/etc/ocms",
		SystemdDir:    "/etc/systemd/system",
		CronFile:      "/etc/cron.d/ocms",
		LogrotateFile: "/etc/logrotate.d/ocms",
		StateDir:      "/run/ocms",
		SitesBase:     "/var/www/vhosts",
		DefaultGroup:  "psaserv",
		BasePort:      8081,
		HealthPath:    "/health",
		ReadyPath:     "/health/ready",
		// internal/config's keys and defaults (TestSettingsFollowTheServerConfig).
		DB:             sitectl.Setting{Name: "db", Env: "OCMS_DB_PATH", Default: "data/ocms.db"},
		Uploads:        sitectl.Setting{Name: "uploads", Env: "OCMS_UPLOADS_DIR", Default: "uploads"},
		Custom:         sitectl.Setting{Name: "custom", Env: "OCMS_CUSTOM_DIR", Default: "custom"},
		ErrorLog:       sitectl.Setting{Name: "errorlog", Env: "OCMS_ERROR_LOG_PATH"},
		Port:           sitectl.Setting{Name: "port", Env: "OCMS_SERVER_PORT", Default: "8080"},
		EnvFileEnv:     "OCMS_ENV_FILE",
		ThemesPath:     "themes",
		ThemeFile:      "theme.json",
		LogsPath:       "logs", // templates/dropin.tmpl
		Unit:           unitTemplate,
		EnvTemplate:    envTemplate,
		DropInTemplate: dropInTemplate,
		ProxyTemplate:  proxyTemplate,
		ScrubSQL:       scrubSQL,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		Exec:           sitectl.RunCmd,
		LookupEnv:      os.LookupEnv,
		HealthTimeout:  30 * time.Second,
		Settle:         3 * time.Second,
		Now:            time.Now,
	}
}

func main() {
	runOnWhenDetached()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, newApp(), os.Args[1:], os.Stdin)
	stop()
	if err != nil {
		// check and status print what they find before they return these.
		if !errors.Is(err, sitectl.ErrSitesDown) && !errors.Is(err, sitectl.ErrReported) {
			_, _ = fmt.Fprintln(os.Stderr, "ocmsctl:", err)
		}
		os.Exit(1)
	}
}

// runOnWhenDetached keeps a command running when the ssh session that
// started it ends: a deploy whose connection drops between stopping and
// starting the sites must still start them. Without this, the lost terminal
// (SIGHUP) or the next line printed to it (SIGPIPE) would end the process.
// The signals are caught, not ignored, so the commands it runs get the usual
// behaviour back.
func runOnWhenDetached() {
	signal.Notify(make(chan os.Signal, 1), syscall.SIGHUP, syscall.SIGPIPE)
}

// parse reads flags wherever they appear among the arguments and returns the
// rest, so `deploy host site --themes` works as well as `deploy --themes ...`.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return rest, nil
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// euid is replaced in tests.
var euid = os.Geteuid

// needRoot guards the commands that change the server.
func needRoot() error {
	if euid() != 0 {
		return errors.New("run this as root")
	}
	return nil
}

func run(ctx context.Context, app *sitectl.App, args []string, stdin io.Reader) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, _ = io.WriteString(app.Stdout, usage)
		return nil
	}
	command := args[0]
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	all := fs.Bool("all", false, "every registered site")
	dryRun := new(bool)
	if command == "bootstrap" || command == "deploy" || command == "pull" {
		fs.BoolVar(dryRun, "dry-run", false, "print the commands instead of running them")
	}
	fs.DurationVar(&app.HealthTimeout, "health-timeout", app.HealthTimeout, "how long a restarted site gets to answer")

	// sites resolves the remaining arguments to registry entries; with
	// defaultAll, no arguments means every site.
	sites := func(rest []string, defaultAll bool) ([]sitectl.Site, error) {
		if len(rest) == 0 && !*all && !defaultAll {
			return nil, errors.New("name at least one site, or use --all")
		}
		return app.Pick(rest, *all || len(rest) == 0)
	}
	one := func(rest []string) (sitectl.Site, error) {
		if len(rest) != 1 {
			return sitectl.Site{}, fmt.Errorf("%s takes exactly one site", command)
		}
		return app.Site(rest[0])
	}

	switch command {
	case "bootstrap":
		arch := fs.String("arch", "amd64", "server architecture")
		ctlBinary := fs.String("ctl", "", "Linux build of ocmsctl (default bin/ocmsctl-linux-<arch>)")
		rest, err := parse(fs, args[1:])
		if err != nil || len(rest) != 1 {
			return errors.Join(err, errors.New("usage: ocmsctl bootstrap <host> [--arch amd64|arm64] [--ctl FILE]"))
		}
		if *ctlBinary == "" {
			*ctlBinary = "bin/ocmsctl-linux-" + *arch
		}
		return app.Bootstrap(ctx, rest[0], *ctlBinary, *dryRun)

	case "deploy":
		o := sitectl.DeployOpts{}
		arch := fs.String("arch", "amd64", "server architecture")
		build := fs.String("build", "", "build command (default: make build-linux-<arch>)")
		noBuild := fs.Bool("no-build", false, "upload the binary as it is")
		fs.StringVar(&o.Binary, "binary", "", "Linux binary to upload (default bin/ocms-linux-<arch>)")
		fs.BoolVar(&o.Themes, "themes", false, "also replace the sites' themes with this checkout's")
		fs.BoolVar(&o.ThemesOnly, "themes-only", false, "replace themes and restart; leave the binary alone")
		timeout := app.HealthTimeout
		rest, err := parse(fs, args[1:])
		if err != nil || len(rest) == 0 {
			return errors.Join(err, errors.New("usage: ocmsctl deploy <host> <site>...|--all [--themes|--themes-only] [--no-build]"))
		}
		if o.Binary == "" {
			o.Binary = "bin/ocms-linux-" + *arch
		}
		if *build == "" {
			*build = "make build-linux-" + *arch
		}
		if !*noBuild {
			o.Build = strings.Fields(*build)
		}
		o.Host, o.Sites, o.All, o.DryRun = rest[0], rest[1:], *all, *dryRun
		if app.HealthTimeout != timeout {
			o.HealthTimeout = app.HealthTimeout
		}
		return app.Deploy(ctx, o)

	case "pull":
		o := sitectl.PullOpts{Confirm: stdin}
		noDB := fs.Bool("no-db", false, "skip the database")
		noUploads := fs.Bool("no-uploads", false, "skip uploads/")
		noLogs := fs.Bool("no-logs", false, "skip logs/")
		fs.BoolVar(&o.Themes, "themes", false, "also copy the themes")
		fs.StringVar(&o.LocalDB, "db", "", "local database to replace (default: the checkout's)")
		fs.IntVar(&o.LocalPort, "port", 0, "local server port that must be free (default: the checkout's); 0 skips the check")
		fs.BoolVar(&o.Yes, "yes", false, "do not ask before overwriting local data")
		rest, err := parse(fs, args[1:])
		if err != nil || len(rest) != 2 {
			return errors.Join(err, errors.New("usage: ocmsctl pull <host> <site> [--no-db] [--no-uploads] [--no-logs] [--themes] [--yes] [--db FILE] [--port N]"))
		}
		fs.Visit(func(f *flag.Flag) { o.NoPortCheck = o.NoPortCheck || f.Name == "port" && o.LocalPort == 0 })
		o.Host, o.Site, o.DryRun = rest[0], rest[1], *dryRun
		o.DB, o.Uploads, o.Logs = !*noDB, !*noUploads, !*noLogs
		return app.Pull(ctx, o)

	case "init":
		if rest, err := parse(fs, args[1:]); err != nil || len(rest) != 0 {
			return errors.Join(err, errors.New("usage: ocmsctl init"))
		}
		if err := needRoot(); err != nil {
			return err
		}
		return app.Init(ctx)

	case "site":
		return runSite(ctx, app, fs, args[1:])

	case "status", "list":
		rest, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		picked, err := sites(rest, true)
		if err != nil {
			return err
		}
		return app.List(ctx, picked)

	case "start", "stop", "restart", "rollback", "install":
		return runLifecycle(ctx, app, command, fs, all, args[1:], stdin)

	case "releases":
		if rest, err := parse(fs, args[1:]); err != nil || len(rest) != 0 {
			return errors.Join(err, errors.New("usage: ocmsctl releases"))
		}
		return app.PrintReleases()

	case "health", "logs", "themes", "snapshot":
		follow := fs.Bool("f", false, "follow the log")
		fs.BoolVar(follow, "follow", false, "follow the log")
		errorsOnly := fs.Bool("errors", false, "show the error log")
		rest, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		site, err := one(rest)
		if err != nil {
			return err
		}
		switch command {
		case "health":
			return app.Health(ctx, site)
		case "logs":
			return app.Logs(ctx, site, 100, *follow, *errorsOnly)
		case "themes":
			return app.InstallThemes(ctx, site, stdin)
		default:
			return app.Snapshot(ctx, site)
		}

	case "backup", "check":
		rest, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if command == "check" {
			if err := needRoot(); err != nil {
				return err
			}
		}
		// Every site (cron's --all) is selected inside, so that a broken
		// registry reaches the alert hook. A wrong site name is the
		// caller's mistake and fails here.
		pick := func() ([]sitectl.Site, error) { return sites(rest, true) }
		if len(rest) > 0 {
			picked, err := sites(rest, false)
			if err != nil {
				return err
			}
			pick = func() ([]sitectl.Site, error) { return picked, nil }
		}
		if command == "backup" {
			return app.Backup(ctx, pick)
		}
		return app.Check(ctx, pick)

	case sitectl.SiteOpCommand:
		// Internal: one site operation, run as the site user by a root
		// ocmsctl. See sitectl.SiteOpCommand.
		if len(args) != 3 {
			return errors.New("usage: ocmsctl _site <operation> <directory>")
		}
		return app.SiteOp(ctx, args[1], args[2], stdin, app.Stdout)

	case sitectl.SendCommand:
		// Internal: the server half of pull's rsync. See sitectl.Send.
		if len(args) < 2 {
			return errors.New("usage: ocmsctl _send <site> <rsync server arguments>")
		}
		site, err := app.Site(args[1])
		if err != nil {
			return err
		}
		return app.Send(ctx, site, args[2:], stdin)
	}
	return fmt.Errorf("unknown command %q (see ocmsctl help)", command)
}

// runLifecycle handles server commands that install or switch running binaries.
func runLifecycle(ctx context.Context, app *sitectl.App, command string, fs *flag.FlagSet, all *bool, args []string, stdin io.Reader) error {
	release := fs.String("to", "", "release to roll back to (default: the one before the current)")
	sum := fs.String("sha256", "", "SHA-256 of the binary on standard input")
	stage := fs.Bool("stage", false, "install: verify and keep the binary, switch to nothing")
	staged := fs.Bool("staged", false, "install: switch to the kept binary with --sha256, read none")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needRoot(); err != nil {
		return err
	}
	if command == "install" && *stage {
		if len(rest) > 0 || *all || *staged {
			return errors.New("install --stage takes no site")
		}
		return app.Stage(stdin, *sum)
	}
	// install may name no site: the binary is installed and running
	// sites keep the old one until they restart.
	var picked []sitectl.Site
	if command != "install" || len(rest) > 0 || *all {
		if len(rest) == 0 && !*all {
			return errors.New("name at least one site, or use --all")
		}
		if picked, err = app.Pick(rest, *all); err != nil {
			return err
		}
	}
	switch command {
	case "start":
		return app.Start(ctx, picked)
	case "stop":
		return app.Stop(ctx, picked)
	case "restart":
		return app.Restart(ctx, picked)
	case "rollback":
		return app.Rollback(ctx, *release, picked)
	}
	if *staged {
		release, err := app.PlaceStaged(*sum)
		if err != nil {
			return err
		}
		return app.Activate(ctx, release, picked)
	}
	return app.Install(ctx, stdin, *sum, picked)
}

// runSite handles registration and per-site provisioning commands.
func runSite(ctx context.Context, app *sitectl.App, fs *flag.FlagSet, args []string) error {
	spec := sitectl.SiteSpec{}
	fs.IntVar(&spec.Port, "port", 0, "port (default: first free one from 8081)")
	fs.StringVar(&spec.Group, "group", "", "group of the site's files (default psaserv)")
	fs.StringVar(&spec.Dir, "dir", "", "site directory (default /var/www/vhosts/<domain>/ocms)")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	switch {
	case len(rest) == 3 && rest[0] == "add":
		if err := needRoot(); err != nil {
			return err
		}
		spec.Domain, spec.User = rest[1], rest[2]
		return app.AddSite(ctx, spec)
	case len(rest) == 2 && rest[0] == "remove":
		if err := needRoot(); err != nil {
			return err
		}
		site, err := app.Site(rest[1])
		if err != nil {
			return err
		}
		return app.RemoveSite(ctx, site)
	case len(rest) == 2 && rest[0] == "show":
		site, err := app.Site(rest[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(app.Stdout, site)
		return err
	case len(rest) == 2 && rest[0] == "paths":
		site, err := app.Site(rest[1])
		if err != nil {
			return err
		}
		return app.SitePaths(ctx, site)
	}
	return errors.New("usage: ocmsctl site add <domain> <user> | site remove <site> | site show <site> | site paths <site>")
}
