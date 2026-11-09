//go:build darwin || linux

// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const signalFixtureEnv = "OCMS_DEV_AUTH_SIGNAL_FIXTURE"

// signalFixtureRunner stands in for run in the child process, so no test here
// can reach the Keychain. It announces that mainExit has installed its signal
// handling, then either waits for the context (as run does) or ignores it (as a
// blocking read of standard input does).
func signalFixtureRunner(mode string) func(context.Context, []string, io.Reader, io.Writer) error {
	return func(ctx context.Context, _ []string, _ io.Reader, output io.Writer) error {
		_, _ = fmt.Fprintln(output, "ready")
		if mode == "ignore-context" {
			time.Sleep(30 * time.Second)
			return errors.New("ran to completion")
		}
		select {
		case <-ctx.Done():
			return errors.New("cancelled by signal")
		case <-time.After(30 * time.Second):
			return errors.New("no signal arrived")
		}
	}
}

// startSignalChild re-executes the test binary as the fixture command and
// returns once its signal handling is installed.
func startSignalChild(t *testing.T, mode string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestShutdownSignalsCancelTheRun$")
	child.Env = append(os.Environ(), signalFixtureEnv+"="+mode)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadString('\n')
		if strings.TrimSpace(line) == "ready" {
			return child, &stderr
		}
		if err != nil {
			_ = child.Wait()
			t.Fatalf("fixture command did not start: %v: %s", err, stderr.String())
		}
	}
}

// This is a subprocess test: the child runs the real mainExit, which must catch
// every shutdown signal. Without the handler the child dies of the signal, so
// the parent sees no clean exit and the test fails.
func TestShutdownSignalsCancelTheRun(t *testing.T) {
	if mode := os.Getenv(signalFixtureEnv); mode != "" {
		os.Exit(mainExit(signalFixtureRunner(mode)))
	}
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			child, stderr := startSignalChild(t, "wait-for-context")
			if err := child.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err := child.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(stderr.String(), "cancelled by signal") {
				t.Fatalf("%s did not cancel the run cleanly: %v: %s", sig, err, stderr.String())
			}
		})
	}
}

// After the first signal, a second one must stop a command that ignores the
// context (importing from a stdin that never closes), as it did before the
// signals were caught.
func TestSecondShutdownSignalTerminatesStuckCommand(t *testing.T) {
	child, _ := startSignalChild(t, "ignore-context")
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case err := <-done:
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("stuck command ended without a signal: %v", err)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
				t.Fatalf("stuck command was not terminated by the repeated signal: %v", err)
			}
			return
		case <-ticker.C:
			_ = child.Process.Signal(syscall.SIGTERM)
		case <-deadline:
			_ = child.Process.Kill()
			t.Fatal("a repeated SIGTERM did not stop a command that ignores its context")
		}
	}
}
