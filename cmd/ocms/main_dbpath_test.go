// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/testutil"
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
	buf := captureDefaultLog(t)

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

// captureDefaultLog sends the default slog logger, and the standard log
// package it takes over, to a buffer until the test ends, then restores
// both.
func captureDefaultLog(t *testing.T) *strings.Builder {
	t.Helper()
	buf := &strings.Builder{}
	previous, writer, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
	return buf
}

// TestServerEventsReachTheEventLog checks the server and seeding events end
// to end: recordServerEvent stores a system event at info level with its
// metadata, and when the Event Log cannot be written each lost event is
// logged by name (EventService.LogEvent logs the failure itself).
//
// Bug state: make recordServerEvent write at another level, or drop the
// failure log in EventService.LogEvent, and this fails.
func TestServerEventsReachTheEventLog(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	events := service.NewEventService(db)
	recordServerEvent(events, "Server started", map[string]any{"addr": "localhost:8099"})
	var level, category string
	var metadata sql.NullString
	if err := db.QueryRow(`SELECT level, category, metadata FROM events WHERE message = 'Server started'`).Scan(&level, &category, &metadata); err != nil {
		t.Fatalf("reading the start event: %v", err)
	}
	if level != model.EventLevelInfo || category != model.EventCategorySystem || !strings.Contains(metadata.String, "localhost:8099") {
		t.Errorf("start event: level=%q category=%q metadata=%q, want an info system event with its address", level, category, metadata.String)
	}

	logs := captureDefaultLog(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	recordServerEvent(events, "Server stopped", nil)
	logSeedingSecuritySignals(context.Background(), &config.Config{DoSeed: true}, events, true)
	if !strings.Contains(logs.String(), `msg="failed to record an event in the Event Log"`) {
		t.Errorf("no failed write was logged:\n%s", logs.String())
	}
	for _, event := range []string{"Server stopped", "Database seeding is enabled", "Default seeded admin credentials are still active"} {
		if !strings.Contains(logs.String(), `event="`+event+`"`) {
			t.Errorf("the lost %q event was not logged by name:\n%s", event, logs.String())
		}
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
// signals only after the port opens, never stops catching them, serves with
// a channel other than the one the signals go to, does not stop with
// stopPolicy{timeout: shutdownTimeout, drain: handlerDrainTimeout} and the
// counter that countInFlight fills right after middleware.Timeout, does not
// record "Server started" in the callback it passes to serveUntilStopped,
// or does not record "Server stopped" after it.
// A port that is taken would leave the process up with nothing listening,
// and a signal sent as the server starts answering would kill it without a
// graceful shutdown.
//
// Bug state: make serveUntilStopped wait on <-quit instead of calling
// waitForStop, move signal.Notify below net.Listen, pass
// make(chan os.Signal, 1) to serveUntilStopped, register countInFlight
// before middleware.Timeout, or delete the "Server stopped" record, and
// this fails.
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
	var runDecl *ast.FuncDecl
	waits := false
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "run" {
			runDecl, calls = fn, firstCalls(fn)
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

	// The server stops with the shared timeout; the start is recorded by the
	// callback serveUntilStopped runs, the stop after it returns.
	serve := calls["serveUntilStopped"]
	if len(serve.Args) != 5 {
		t.Fatalf("serveUntilStopped takes %d arguments in run, want 5", len(serve.Args))
	}
	// The stop uses the shared timeouts and the counter that countInFlight
	// fills right after middleware.Timeout in run's router.
	policy := map[string]string{}
	if lit, ok := serve.Args[3].(*ast.CompositeLit); ok {
		for _, elt := range lit.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok {
					policy[key.Name] = exprString(kv.Value)
				}
			}
		}
	}
	if policy["timeout"] != "shutdownTimeout" || policy["drain"] != "handlerDrainTimeout" || !strings.HasPrefix(policy["inflight"], "&") {
		t.Errorf("run stops the server with %v, want stopPolicy{timeout: shutdownTimeout, drain: handlerDrainTimeout, inflight: &<counter>}", policy)
	}
	var follows string
	ast.Inspect(runDecl.Body, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		for i, stmt := range block.List {
			if strings.HasPrefix(exprString(stmtCall(stmt)), "r.Use(middleware.Timeout(") && i+1 < len(block.List) {
				follows = exprString(stmtCall(block.List[i+1]))
			}
		}
		return true
	})
	if want := "r.Use(countInFlight(" + policy["inflight"] + "))"; follows != want {
		t.Errorf("run registers %q right after middleware.Timeout, want %q: handlers Timeout stops waiting for would go uncounted", follows, want)
	}
	if started, ok := serve.Args[4].(*ast.FuncLit); !ok || recordedEvent(started, "Server started") == nil {
		t.Error(`run does not record "Server started" in the callback it passes to serveUntilStopped`)
	}
	if stop := recordedEvent(runDecl, "Server stopped"); stop == nil || stop.Pos() < serve.End() {
		t.Error(`run does not record "Server stopped" after serveUntilStopped returns`)
	}
}

// exprString renders expr as Go source, or "" for nil.
func exprString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var buf strings.Builder
	if err := format.Node(&buf, token.NewFileSet(), expr); err != nil {
		return ""
	}
	return buf.String()
}

// stmtCall returns the call an expression statement makes, or nil.
func stmtCall(stmt ast.Stmt) ast.Expr {
	if expr, ok := stmt.(*ast.ExprStmt); ok {
		if call, ok := expr.X.(*ast.CallExpr); ok {
			return call
		}
	}
	return nil
}

// recordedEvent returns the first recordServerEvent call in node that
// records message, or nil.
func recordedEvent(node ast.Node, message string) *ast.CallExpr {
	var found *ast.CallExpr
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found != nil || len(call.Args) < 2 {
			return found == nil
		}
		if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "recordServerEvent" {
			if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Value == strconv.Quote(message) {
				found = call
			}
		}
		return found == nil
	})
	return found
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

// TestServeUntilStopped runs a real server: the server answers while the
// started callback runs, a slow started holds up neither the stop nor the
// close of the port but is waited for before returning, a stop signal waits
// for the request in flight, then returns nil and closes the port, a
// request still running at the shutdown timeout has its connection closed
// and its context cancelled and is waited for, up to the drain limit, and a
// server that cannot accept returns its error. A hang fails the test after
// ten seconds instead of running into the test binary's timeout.
//
// Bug state: shut down with srv.Close instead of srv.Shutdown, never close
// serveErr, call started before Serve starts or before waiting for the
// signal, run it without waiting for it, return the Shutdown error without
// closing the server, or skip waitForHandlers, and this fails.
func TestServeUntilStopped(t *testing.T) {
	listen := func(t *testing.T) net.Listener {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return listener
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
	serve := func(srv *http.Server, listener net.Listener, quit <-chan os.Signal, stop stopPolicy, started func()) <-chan error {
		done := make(chan error, 1)
		go func() { done <- serveUntilStopped(srv, listener, quit, stop, started) }()
		return done
	}
	get := func(listener net.Listener) {
		if resp, err := http.Get("http://" + listener.Addr().String() + "/"); err == nil {
			_ = resp.Body.Close()
		}
	}
	// pinging returns a started callback that requests /ping, and the
	// channel its result arrives on.
	pinging := func(listener net.Listener) (func(), <-chan error) {
		answered := make(chan error, 1)
		client := &http.Client{Timeout: 2 * time.Second}
		return func() {
			resp, err := client.Get("http://" + listener.Addr().String() + "/ping")
			if err == nil {
				err = resp.Body.Close()
			}
			answered <- err
		}, answered
	}
	accepts := func(listener net.Listener) bool {
		conn, err := net.Dial("tcp", listener.Addr().String())
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	}

	listener := listen(t)
	started, release := make(chan error), make(chan struct{})
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			return
		}
		close(started)
		<-release
		_, _ = io.WriteString(w, "done")
	})}
	quit := make(chan os.Signal, 1)
	ping, answered := pinging(listener)
	done := serve(srv, listener, quit, stopPolicy{timeout: time.Minute}, ping)
	if err := within(t, "the started callback's request", answered); err != nil {
		t.Errorf("the server did not answer while started ran: %v", err)
	}
	inFlight := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/")
		if err != nil {
			inFlight <- err
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err == nil && string(body) != "done" {
			err = errors.New("unexpected body " + strconv.Quote(string(body)))
		}
		inFlight <- err
	}()
	_ = within(t, "the request reaching the handler", started)
	quit <- os.Interrupt
	select {
	case err := <-done:
		t.Fatalf("serveUntilStopped returned %v with a request in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := within(t, "the response", inFlight); err != nil {
		t.Errorf("the request in flight during the stop: %v", err)
	}
	if err := within(t, "the stop", done); err != nil {
		t.Fatalf("serveUntilStopped after a signal = %v, want nil", err)
	}
	if accepts(listener) {
		t.Error("the server still accepts connections after a stop signal")
	}

	// A started callback that blocks (a locked database) does not hold up
	// the stop, but serveUntilStopped returns only once it is done.
	listener = listen(t)
	unblock, entered := make(chan struct{}), make(chan error)
	quit = make(chan os.Signal, 1)
	done = serve(&http.Server{ReadHeaderTimeout: time.Second}, listener, quit, stopPolicy{timeout: time.Minute}, func() {
		close(entered)
		<-unblock
	})
	_ = within(t, "the started callback", entered)
	quit <- os.Interrupt
	closed := make(chan error, 1)
	go func() {
		for accepts(listener) {
			time.Sleep(10 * time.Millisecond)
		}
		closed <- nil
	}()
	_ = within(t, "the port closing while started blocks", closed)
	select {
	case err := <-done:
		t.Fatalf("serveUntilStopped returned %v before started finished", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(unblock)
	if err := within(t, "the stop after started finished", done); err != nil {
		t.Fatalf("serveUntilStopped with a slow started = %v, want nil", err)
	}

	// A request that outlives the shutdown timeout has its connection closed,
	// which cancels its context; the stop waits for its handler to return,
	// and the timeout is reported.
	listener = listen(t)
	var running atomic.Int64
	stuck, cancelled, finished := make(chan error), make(chan error, 1), make(chan struct{})
	srv = &http.Server{ReadHeaderTimeout: time.Second, Handler: countInFlight(&running)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(stuck)
		select {
		case <-r.Context().Done():
			cancelled <- nil
		case <-time.After(10 * time.Second):
		}
		time.Sleep(100 * time.Millisecond) // cleanup after the cancel
		close(finished)
	}))}
	quit = make(chan os.Signal, 1)
	done = serve(srv, listener, quit, stopPolicy{timeout: 100 * time.Millisecond, drain: time.Second, inflight: &running}, func() {})
	go get(listener)
	_ = within(t, "the stuck request reaching the handler", stuck)
	quit <- os.Interrupt
	if err := within(t, "the stop", done); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("serveUntilStopped past the shutdown timeout = %v, want the deadline", err)
	}
	_ = within(t, "the stuck request's context being cancelled", cancelled)
	select {
	case <-finished:
	default:
		t.Error("serveUntilStopped returned before the handler of the closed connection did")
	}

	// A handler that ignores its cancelled context holds up the stop only
	// for the drain limit.
	listener = listen(t)
	var hungRunning atomic.Int64
	hung, release2 := make(chan error), make(chan struct{})
	t.Cleanup(func() { close(release2) })
	srv = &http.Server{ReadHeaderTimeout: time.Second, Handler: countInFlight(&hungRunning)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(hung)
		<-release2
	}))}
	quit = make(chan os.Signal, 1)
	done = serve(srv, listener, quit, stopPolicy{timeout: 100 * time.Millisecond, drain: 50 * time.Millisecond, inflight: &hungRunning}, func() {})
	go get(listener)
	_ = within(t, "the hung request reaching the handler", hung)
	stopped := time.Now()
	quit <- os.Interrupt
	if err := within(t, "the stop past the drain limit", done); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("serveUntilStopped with a hung handler = %v, want the deadline", err)
	}
	if elapsed := time.Since(stopped); elapsed > time.Second {
		t.Errorf("a hung handler held up the stop for %v; the 100 ms timeout and 50 ms drain allow well under a second", elapsed)
	}

	// middleware.Timeout answers a slow request and stops waiting for it,
	// while its handler keeps running in Timeout's goroutine; Shutdown then
	// succeeds at once, and the stop still waits for that handler through
	// the count kept inside Timeout.
	listener = listen(t)
	var slowRunning atomic.Int64
	slowEntered, slowDone := make(chan error), make(chan struct{})
	srv = &http.Server{ReadHeaderTimeout: time.Second, Handler: middleware.Timeout(100 * time.Millisecond)(countInFlight(&slowRunning)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(slowEntered)
		time.Sleep(400 * time.Millisecond) // ignores its context
		close(slowDone)
	})))}
	quit = make(chan os.Signal, 1)
	done = serve(srv, listener, quit, stopPolicy{timeout: 5 * time.Second, drain: 2 * time.Second, inflight: &slowRunning}, func() {})
	timedOut := make(chan error, 1)
	go func() { get(listener); timedOut <- nil }()
	_ = within(t, "the slow request reaching the handler", slowEntered)
	_ = within(t, "the request timeout's answer", timedOut)
	quit <- os.Interrupt
	if err := within(t, "the stop after a request timeout", done); err != nil {
		t.Errorf("serveUntilStopped after a request timeout = %v, want nil", err)
	}
	select {
	case <-slowDone:
	default:
		t.Error("serveUntilStopped returned while the handler behind the request timeout still ran")
	}

	// A closed listener makes Serve fail at once, like an accept error.
	listener = listen(t)
	_ = listener.Close()
	if err := within(t, "the failure", serve(&http.Server{ReadHeaderTimeout: time.Second}, listener, make(chan os.Signal), stopPolicy{timeout: time.Minute}, func() {})); err == nil {
		t.Fatal("serveUntilStopped returned nil for a server that cannot accept")
	}
}
