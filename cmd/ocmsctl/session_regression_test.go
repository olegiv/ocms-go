// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/sitectl"
)

// pull says where it writes, with where each path came from, before it
// writes anything, and mirrors uploads only inside the checkout.
func TestPullSaysWhereItWritesAndStaysInside(t *testing.T) {
	w := newWorld(t)
	serverDB(t, filepath.Join(w.addSite(t), "data", "ocms.db"), "production")
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, ".", map[string]string{".env": "OCMS_DB_PATH=./db/dev.db\n"})
	w.environ["OCMS_UPLOADS_DIR"] = "./media"
	if err := run(context.Background(), w.local, []string{"pull", "root@server", "example_com", "--yes", "--no-logs", "--port", "0"}, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	for _, want := range []string{
		"database  " + filepath.Join(cwd, "db", "dev.db") + " (.env)",
		"uploads   " + filepath.Join(cwd, "media") + " (the environment), mirrored",
	} {
		if !strings.Contains(w.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, w.out)
		}
	}

	// A value another project left in the environment.
	elsewhere := t.TempDir()
	w.environ["OCMS_UPLOADS_DIR"] = elsewhere
	w.rsync = nil
	err = run(context.Background(), w.local, []string{"pull", "root@server", "example_com", "--yes", "--no-db", "--no-logs"}, nil)
	if err == nil || !strings.Contains(err.Error(), "outside this checkout") || len(w.rsync) != 0 {
		t.Errorf("uploads outside the checkout: err = %v, rsync %v", err, w.rsync)
	}
}

// Only an ocmsctl on the server that lacks `site paths` gets the advice to
// update it.
func TestPullAdvisesUpdatingOnlyAnOldServer(t *testing.T) {
	w := newWorld(t)
	w.addSite(t)
	t.Chdir(t.TempDir())
	exec := w.local.Exec
	w.local.Exec = func(ctx context.Context, c sitectl.Cmd) error {
		if c.Name == "ssh" && strings.Contains(strings.Join(c.Args, " "), "site paths") {
			_, _ = fmt.Fprintln(c.Stderr, "ocmsctl: usage: ocmsctl site add <domain> <user> | site remove <site> | site show <site>")
			return errors.New("exit status 1")
		}
		return exec(ctx, c)
	}
	err := run(context.Background(), w.local, []string{"pull", "root@server", "example_com", "--yes", "--no-db", "--no-logs"}, nil)
	if err == nil || !strings.Contains(err.Error(), "make server-setup") {
		t.Errorf("old server: %v", err)
	}
}

// The server's ocmsctl finishes what it started when the ssh session that
// started it ends: neither a lost terminal (SIGHUP) nor output to a closed
// connection (SIGPIPE) may stop a restart between stopping and starting the
// sites.
func TestTheServerRunsOnWhenItsSessionEnds(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Not the developer's OCMS_* settings: the tool would read them.
	environ := []string{"OCMSCTL_TEST_MAIN=1"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "OCMS_") {
			environ = append(environ, kv)
		}
	}
	tool := func(args ...string) *exec.Cmd {
		cmd := exec.Command(self, args...)
		cmd.Env = environ
		cmd.Dir = t.TempDir()
		return cmd
	}
	signaled := func(err error) bool {
		var exit *exec.ExitError
		return errors.As(err, &exit) && exit.Sys().(syscall.WaitStatus).Signaled()
	}

	// Output to a connection whose reader is gone.
	r, pipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	help := tool("help")
	help.Stdout = pipe
	err = help.Run()
	_ = pipe.Close()
	if err != nil {
		t.Errorf("writing to a closed connection: %v (signaled: %v)", err, signaled(err))
	}

	// The terminal hangs up while a command waits: pull's question.
	pull := tool("pull", "root@server", "example_com", "--no-db", "--no-uploads", "--no-logs")
	answer, err := pull.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := pull.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pull.Stderr = io.Discard
	if err := pull.Start(); err != nil {
		t.Fatal(err)
	}
	asked := make(chan bool, 1)
	go func() {
		reader := bufio.NewReader(out)
		for {
			line, err := reader.ReadString('?')
			if strings.Contains(line, "Continue?") {
				asked <- true
				_, _ = io.Copy(io.Discard, reader)
				return
			}
			if err != nil {
				asked <- false
				return
			}
		}
	}()
	select {
	case ok := <-asked:
		if !ok {
			t.Fatal("pull did not ask")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pull did not ask")
	}
	if err := pull.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	_, _ = io.WriteString(answer, "n\n")
	_ = answer.Close()
	err = pull.Wait()
	if signaled(err) {
		t.Errorf("a hangup ended the command: %v", err)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Errorf("pull after the answer n = %v, want the refusal (exit 1)", err)
	}
}
