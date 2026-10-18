// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSQLiteFilePathFromDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dsn  string
		want string
		ok   bool
	}{
		{name: "plain path", dsn: "./data/ocms.db", want: "./data/ocms.db", ok: true},
		{name: "memory dsn", dsn: ":memory:", want: "", ok: false},
		{name: "file memory uri", dsn: "file::memory:?cache=shared", want: "", ok: false},
		{name: "file memory mode uri", dsn: "file:ignored.db?mode=memory&cache=shared", want: "", ok: false},
		{name: "file absolute uri", dsn: "file:/var/db/ocms.db", want: "/var/db/ocms.db", ok: true},
		{name: "file relative uri", dsn: "file:./data/ocms.db", want: "./data/ocms.db", ok: true},
		{name: "file escaped uri", dsn: "file:/tmp/ocms%20prod.db", want: "/tmp/ocms prod.db", ok: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := sqliteFilePathFromDSN(tc.dsn)
			if ok != tc.ok {
				t.Fatalf("ok mismatch: got %v want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("path mismatch: got %q want %q", got, tc.want)
			}
		})
	}
}

// TestOpenErrorLog checks the optional error log: no file without a path,
// and owner-only permissions even on a file that already existed with a
// wider mode (OpenFile applies the mode only when it creates the file).
func TestOpenErrorLog(t *testing.T) {
	t.Parallel()

	if file, err := openErrorLog(""); file != nil || err != nil {
		t.Fatalf("openErrorLog(\"\") = %v, %v; want no file", file, err)
	}

	logPath := filepath.Join(t.TempDir(), "error.log")
	if err := os.WriteFile(logPath, []byte("earlier\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// WriteFile's mode is filtered by the umask; set it outright.
	if err := os.Chmod(logPath, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := openErrorLog(logPath)
	if err != nil {
		t.Fatalf("openErrorLog: %v", err)
	}
	defer closeErrorLog(file)
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("error log mode = %v, want 0600", mode)
	}
	if _, err := openErrorLog(filepath.Join(t.TempDir(), "missing", "error.log")); err == nil {
		t.Error("openErrorLog succeeded in a directory that does not exist")
	}
}

// TestWithErrorLog checks that the error log receives ERROR records only,
// while every record still reaches the base handler.
func TestWithErrorLog(t *testing.T) {
	t.Parallel()

	file, err := openErrorLog(filepath.Join(t.TempDir(), "error.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeErrorLog(file)
	var base strings.Builder
	logger := slog.New(withErrorLog(slog.NewTextHandler(&base, nil), file))
	logger.Info("routine startup")
	logger.Error("disk full")

	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "disk full") || strings.Contains(got, "routine startup") {
		t.Errorf("error log = %q, want only the ERROR record", got)
	}
	if !strings.Contains(base.String(), "routine startup") || !strings.Contains(base.String(), "disk full") {
		t.Errorf("base handler = %q, want both records", base.String())
	}
	if withErrorLog(slog.NewTextHandler(&base, nil), nil) == nil {
		t.Error("withErrorLog without a file returned no handler")
	}
}

// TestOpenDatabase checks that the database file and its WAL and
// shared-memory files end up owner-only even when the database already
// existed with a wider mode (SQLite gives the sidecars that mode on open),
// and that migrations ran.
func TestOpenDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ocms.db")
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v (the database runs in WAL mode, so the file should exist)", filepath.Base(path), err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %v, want 0600", filepath.Base(path), mode)
		}
	}
	var pages int
	if err := db.QueryRow("SELECT COUNT(*) FROM pages").Scan(&pages); err != nil {
		t.Errorf("migrations did not run: %v", err)
	}
}

// TestLogRunFailure checks that a run failure is logged once, by the first
// deferred call to see it, and marked so main does not log it again after
// run's deferred closes have shut the error log file and the event log.
func TestLogRunFailure(t *testing.T) {
	var buf strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	runErr := errors.New("running migrations: disk I/O error")
	logRunFailure(&runErr)
	logRunFailure(&runErr)
	if n := strings.Count(buf.String(), "application exited with error"); n != 1 {
		t.Errorf("failure logged %d times, want once:\n%s", n, buf.String())
	}
	var logged loggedRunError
	if !errors.As(runErr, &logged) || !strings.Contains(runErr.Error(), "disk I/O error") {
		t.Errorf("runErr = %v, want the original error marked as logged", runErr)
	}

	var none error
	logRunFailure(&none)
	if none != nil || strings.Count(buf.String(), "application exited with error") != 1 {
		t.Error("a nil error was logged or wrapped")
	}
}

// TestRunLogsFailuresBeforeClosingLogs fails when a failure path in run can
// return after the error log file or the database close defer is registered
// but before a defer logRunFailure(&runErr) follows it. Defers run
// last-in, first-out, so without that defer the failure reaches main only
// after both have closed, and is logged to stdout alone. The scan walks run's
// statements in order: every close defer must be followed by a logRunFailure
// defer before the next statement that can return.
//
// Bug state: delete the first defer logRunFailure(&runErr), after the logger
// is set up, and this names the openDatabase return before the second one.
func TestRunLogsFailuresBeforeClosingLogs(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var run *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "run" {
			run = fn
		}
	}
	if run == nil {
		t.Fatal("no run function in main.go")
	}
	closes := 0
	var pending []string // closes registered since the last logRunFailure defer
	for _, stmt := range run.Body.List {
		if deferStmt, ok := stmt.(*ast.DeferStmt); ok {
			switch name := deferredCloser(deferStmt); name {
			case "logRunFailure":
				pending = nil
			case "":
			default:
				closes++
				pending = append(pending, name)
			}
			continue
		}
		if len(pending) > 0 && containsReturn(stmt) {
			t.Errorf("%s can return after %v closes on return but before a defer logRunFailure(&runErr) follows",
				fset.Position(stmt.Pos()), pending)
			pending = nil
		}
	}
	if closes < 2 {
		t.Fatalf("found %d close defers in run, want the error log file and the database; the scan is stale", closes)
	}
}

// deferredCloser names what a defer in run does: "logRunFailure", the log
// destination it closes ("the error log file", "the database"), or "".
func deferredCloser(deferStmt *ast.DeferStmt) string {
	switch fun := deferStmt.Call.Fun.(type) {
	case *ast.Ident:
		switch fun.Name {
		case "logRunFailure":
			return "logRunFailure"
		case "closeErrorLog":
			return "the error log file"
		}
	case *ast.FuncLit:
		closesDB := false
		ast.Inspect(fun.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Close" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "db" {
					closesDB = true
				}
			}
			return true
		})
		if closesDB {
			return "the database"
		}
	}
	return ""
}

// containsReturn reports whether stmt can return from the enclosing function:
// it contains a return statement outside any function literal.
func containsReturn(stmt ast.Stmt) bool {
	found := false
	ast.Inspect(stmt, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			found = true
		}
		return !found
	})
	return found
}

// TestRunServerLifecycle fails when run stops binding the listener before
// it serves, stops serving through serveUntilStopped, registers for stop
// signals only after the port opens, never stops catching them, or serves
// with a channel other than the one the signals go to. A port that is taken
// would leave the process up with nothing listening, and a signal sent as
// the server starts answering would kill it without a graceful shutdown.
//
// Bug state: make serveUntilStopped wait on <-quit instead of calling
// waitForStop, move signal.Notify below net.Listen, or pass
// make(chan os.Signal, 1) to serveUntilStopped, and this fails.
func TestRunServerLifecycle(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// firstCalls maps each function fn calls to its first call.
	firstCalls := func(fn *ast.FuncDecl) map[string]*ast.CallExpr {
		found := map[string]*ast.CallExpr{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				if pkg, ok := fun.X.(*ast.Ident); ok {
					name = pkg.Name + "." + fun.Sel.Name
				}
			}
			if _, seen := found[name]; name != "" && !seen {
				found[name] = call
			}
			return true
		})
		return found
	}
	var calls map[string]*ast.CallExpr
	waits := false
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "run" {
			calls = firstCalls(fn)
		} else if ok && fn.Name.Name == "serveUntilStopped" {
			_, waits = firstCalls(fn)["waitForStop"]
		}
	}
	if !waits {
		t.Error("serveUntilStopped does not wait through waitForStop")
	}
	for _, want := range []string{"net.Listen", "serveUntilStopped", "signal.Notify", "signal.Stop"} {
		if calls[want] == nil {
			t.Fatalf("run does not call %s", want)
		}
	}
	if notify, listen := calls["signal.Notify"].Pos(), calls["net.Listen"].Pos(); notify > listen {
		t.Errorf("run registers for stop signals at %s, after it opens the port at %s",
			fset.Position(notify), fset.Position(listen))
	}
	// argName returns the identifier passed as argument i, or "".
	argName := func(call *ast.CallExpr, i int) string {
		if len(call.Args) > i {
			if ident, ok := call.Args[i].(*ast.Ident); ok {
				return ident.Name
			}
		}
		return ""
	}
	signals := argName(calls["signal.Notify"], 0)
	if served := argName(calls["serveUntilStopped"], 2); signals == "" || served != signals {
		t.Errorf("run catches stop signals on %q but serves until %q receives one", signals, served)
	}
	if stopped := argName(calls["signal.Stop"], 0); stopped != signals {
		t.Errorf("run stops catching signals on %q, not on %q", stopped, signals)
	}
}

// TestWaitForStop checks how serving ends: a failure is returned without a
// shutdown, a signal shuts down cleanly, a failed shutdown is reported, and
// a failure that arrives together with a signal is still returned. That
// last case used to exit as a clean stop, because only the signal was read.
//
// Bug state: return nil after shutdown instead of reading serveErr, and the
// "failure during the shutdown" case fails.
func TestWaitForStop(t *testing.T) {
	failure := errors.New("accept: too many open files")
	// served returns a serveErr channel for a server that has ended, with
	// its failure if there was one.
	served := func(err error) chan error {
		ch := make(chan error, 1)
		if err != nil {
			ch <- err
		}
		close(ch)
		return ch
	}
	signaled := func() chan os.Signal {
		ch := make(chan os.Signal, 1)
		ch <- os.Interrupt
		return ch
	}

	shutdowns := 0
	if err := waitForStop(make(chan os.Signal), served(failure), func() error { shutdowns++; return nil }); !errors.Is(err, failure) || shutdowns != 0 {
		t.Errorf("failure: err = %v after %d shutdowns, want the failure and no shutdown", err, shutdowns)
	}
	shutdowns = 0
	if err := waitForStop(signaled(), served(nil), func() error { shutdowns++; return nil }); err != nil || shutdowns != 1 {
		t.Errorf("signal: err = %v after %d shutdowns, want nil after one", err, shutdowns)
	}
	stuck := errors.New("context deadline exceeded")
	if err := waitForStop(signaled(), served(nil), func() error { return stuck }); !errors.Is(err, stuck) {
		t.Errorf("failed shutdown: err = %v, want it reported", err)
	}
	// Only the signal is ready at first; the failure lands during the
	// shutdown, as when Serve fails just as the signal arrives.
	serveErr := make(chan error, 1)
	if err := waitForStop(signaled(), serveErr, func() error {
		serveErr <- failure
		close(serveErr)
		return nil
	}); !errors.Is(err, failure) {
		t.Errorf("failure during the shutdown: err = %v, want the failure", err)
	}
	// With both ready, select picks either; repeat so both branches run.
	for i := range 100 {
		if err := waitForStop(signaled(), served(failure), func() error { return nil }); !errors.Is(err, failure) {
			t.Fatalf("failure with a signal, attempt %d: err = %v, want the failure", i, err)
		}
	}
}

// TestServeUntilStopped runs a real server: a stop signal waits for the
// request in flight, then returns nil and closes the port, and a server
// that cannot accept returns its error. A hang fails the test after ten
// seconds instead of running into the test binary's timeout.
//
// Bug state: shut down with srv.Close instead of srv.Shutdown, or never
// close serveErr, and this fails.
func TestServeUntilStopped(t *testing.T) {
	listen := func(t *testing.T) net.Listener {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return listener
	}
	serve := func(srv *http.Server, listener net.Listener, quit <-chan os.Signal) <-chan error {
		done := make(chan error, 1)
		go func() { done <- serveUntilStopped(srv, listener, quit) }()
		return done
	}
	within := func(t *testing.T, what string, ch <-chan error) error {
		t.Helper()
		select {
		case err := <-ch:
			return err
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not happen within ten seconds", what)
			return nil
		}
	}

	listener := listen(t)
	started, release := make(chan error), make(chan struct{})
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "done")
	})}
	quit := make(chan os.Signal, 1)
	done := serve(srv, listener, quit)
	answered := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/")
		if err != nil {
			answered <- err
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err == nil && string(body) != "done" {
			err = errors.New("unexpected body " + strconv.Quote(string(body)))
		}
		answered <- err
	}()
	_ = within(t, "the request reaching the handler", started)
	quit <- os.Interrupt
	select {
	case err := <-done:
		t.Fatalf("serveUntilStopped returned %v with a request in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := within(t, "the response", answered); err != nil {
		t.Errorf("the request in flight during the stop: %v", err)
	}
	if err := within(t, "the stop", done); err != nil {
		t.Fatalf("serveUntilStopped after a signal = %v, want nil", err)
	}
	if conn, err := net.Dial("tcp", listener.Addr().String()); err == nil {
		_ = conn.Close()
		t.Error("the server still accepts connections after a stop signal")
	}

	// A closed listener makes Serve fail at once, like an accept error.
	listener = listen(t)
	_ = listener.Close()
	if err := within(t, "the failure", serve(&http.Server{ReadHeaderTimeout: time.Second}, listener, make(chan os.Signal))); err == nil {
		t.Fatal("serveUntilStopped returned nil for a server that cannot accept")
	}
}
