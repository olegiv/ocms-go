// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// ocms-dev-auth shares a development authenticator between local coding agents.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"
)

const usage = "usage: ocms-dev-auth import|status|code --profile mcp-staging|local --account EMAIL [--origin LOOPBACK_ORIGIN]"

func main() {
	os.Exit(mainExit(run))
}

// mainExit runs execute under a context that ends on any of shutdownSignals or
// after the 55-second deadline, and returns the process exit code.
func mainExit(execute func(context.Context, []string, io.Reader, io.Writer) error) int {
	signalCtx, stop := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer stop()
	go func() {
		// After the first signal, restore default handling so that a second one
		// still ends a command that is blocked outside the context (stdin).
		<-signalCtx.Done()
		stop()
	}()
	ctx, cancel := context.WithTimeout(signalCtx, 55*time.Second)
	defer cancel()
	if err := execute(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	command := args[0]
	if command != "import" && command != "status" && command != "code" {
		return errors.New(usage)
	}
	flags := flag.NewFlagSet("ocms-dev-auth", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // Never echo unknown flags or their values.
	profile := flags.String("profile", "", "mcp-staging or local")
	account := flags.String("account", "", "dedicated development account email")
	origin := flags.String("origin", "", "local profile's loopback origin")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return errors.New(usage)
	}
	target, err := resolveTarget(*profile, *origin, *account)
	if err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return errors.New("cannot locate the user cache directory")
	}
	s := newAuthenticator(keychainStore{}, cache)
	var result any
	switch command {
	case "import":
		if file, ok := input.(*os.File); ok {
			info, statErr := file.Stat()
			if statErr != nil || info.Mode()&os.ModeCharDevice != 0 {
				return errors.New("supply enrollment data through a private stdin pipe, not the terminal or command arguments")
			}
		}
		data, readErr := io.ReadAll(io.LimitReader(input, 4097))
		if readErr != nil || len(data) > 4096 {
			return errors.New("cannot read enrollment data (maximum 4096 bytes)")
		}
		if importErr := s.importSeed(ctx, target, string(data)); importErr != nil {
			return importErr
		}
		result = statusResult{Configured: true, Origin: target.Origin, Account: target.Account}
	case "status":
		result, err = s.status(ctx, target)
	case "code":
		result, err = s.issue(ctx, target)
	}
	if err != nil {
		return err
	}
	return deliver(ctx, output, result)
}

// deliver writes the JSON result. A private code directory is removed again
// when the consumer will never learn of it: the run was cancelled (signal or
// deadline) after the code was written, or the result cannot be written.
func deliver(ctx context.Context, output io.Writer, result any) error {
	code, isCode := result.(codeResult)
	if isCode && ctx.Err() != nil {
		_ = os.RemoveAll(code.directory)
		return errors.New("interrupted before the development code was delivered")
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		if isCode {
			_ = os.RemoveAll(code.directory)
		}
		return errors.New("cannot write authenticator result")
	}
	return nil
}
