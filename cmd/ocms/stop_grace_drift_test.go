// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestStopGracePeriodsCoverTheDrain fails when a shipped deployment (Fly,
// Docker Compose, the systemd unit or ocmsctl) gives the app less time to
// stop than the request drain (shutdownTimeout), the wait for handlers still
// running (handlerDrainTimeout), a margin for the "Server stopped" event,
// which can wait out the database's 5-second busy timeout, the shared
// budget of the shutdown steps (cleanupTimeout), the Event Log close
// (eventLogCloseTimeout), and a second for the shortest waits of steps after
// a spent budget and the exit.
// It also fails when a deployment stops the app with a signal it does not
// catch, or when the container entrypoint does not exec the app, which would
// leave the shell as PID 1 to swallow the signal. Fly's default kill_timeout
// is 5 seconds, Docker's stop grace period 10, and the unit and ocmsctl
// allowed 30, so all of them killed the app mid-drain (review of the
// 2026-10-17 audit fixes).
//
// Bug state: lower kill_timeout in fly.toml, stop_grace_period in
// docker-compose.yml, TimeoutStopSec in scripts/deploy/ocms@.service or the
// wait in scripts/deploy/ocmsctl below 54 seconds and this fails.
func TestStopGracePeriodsCoverTheDrain(t *testing.T) {
	need := shutdownTimeout + handlerDrainTimeout + 5*time.Second + cleanupTimeout + eventLogCloseTimeout + time.Second
	root := filepath.Join("..", "..")
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name)) // #nosec G304 -- fixed repository paths
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	caught := map[string]bool{"": true, "SIGINT": true, "SIGTERM": true}
	check := func(where string, grace time.Duration, signal string) {
		t.Helper()
		if grace < need {
			t.Errorf("%s gives the app %v to stop, want at least %v", where, grace, need)
		}
		if !caught[strings.Trim(signal, `"'`)] {
			t.Errorf("%s stops the app with %s, which it does not catch; use SIGTERM or SIGINT", where, signal)
		}
	}

	// fly.toml: top-level keys come before the first [table] header.
	fly := read("fly.toml")
	topLevel := fly
	if table := regexp.MustCompile(`(?m)^\s*\[`).FindStringIndex(fly); table != nil {
		topLevel = fly[:table[0]]
	}
	setting := func(key string) string {
		m := regexp.MustCompile(`(?m)^` + key + `\s*=\s*('[^']*'|"[^"]*"|\S+)\s*(#.*)?$`).FindStringSubmatch(topLevel)
		if m == nil {
			return ""
		}
		return strings.Trim(m[1], `"'`)
	}
	killTimeout := setting("kill_timeout")
	var flyGrace time.Duration
	if seconds, err := strconv.Atoi(killTimeout); err == nil {
		flyGrace = time.Duration(seconds) * time.Second
	} else if d, err := time.ParseDuration(killTimeout); err == nil {
		flyGrace = d
	}
	if killTimeout == "" {
		t.Error("fly.toml sets no top-level kill_timeout; Fly's default of 5 seconds kills the app mid-drain")
	} else {
		check("fly.toml kill_timeout", flyGrace, setting("kill_signal"))
	}

	var compose struct {
		Services map[string]struct {
			StopGracePeriod string `yaml:"stop_grace_period"`
			StopSignal      string `yaml:"stop_signal"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(read("docker-compose.yml")), &compose); err != nil {
		t.Fatalf("parsing docker-compose.yml: %v", err)
	}
	service, ok := compose.Services["ocms"]
	if !ok {
		t.Fatal("docker-compose.yml has no ocms service; the layout changed and this test is vacuous")
	}
	composeGrace, err := time.ParseDuration(service.StopGracePeriod)
	if err != nil {
		t.Errorf("docker-compose.yml ocms stop_grace_period = %q: %v", service.StopGracePeriod, err)
	}
	check("docker-compose.yml ocms stop_grace_period", composeGrace, service.StopSignal)

	unit := read("scripts/deploy/ocms@.service")
	unitValue := func(key string) string {
		m := regexp.MustCompile(`(?m)^` + key + `=(\S+)\s*$`).FindStringSubmatch(unit)
		if m == nil {
			return ""
		}
		return m[1]
	}
	stopSec, err := strconv.Atoi(unitValue("TimeoutStopSec"))
	if err != nil {
		t.Errorf("scripts/deploy/ocms@.service sets no TimeoutStopSec in seconds; systemd's default of 90 seconds is not pinned")
	}
	check("scripts/deploy/ocms@.service TimeoutStopSec", time.Duration(stopSec)*time.Second, unitValue("KillSignal"))

	ctl := read("scripts/deploy/ocmsctl")
	wait := regexp.MustCompile(`is_pid_running "\$pid" && \[ \$waited -lt (\d+) \]`).FindStringSubmatch(ctl)
	if wait == nil {
		t.Fatal("scripts/deploy/ocmsctl has no graceful-stop wait loop; the script changed and this test is vacuous")
	}
	waitSec, _ := strconv.Atoi(wait[1])
	signal := regexp.MustCompile(`kill -(SIG\w+) "\$pid"`).FindStringSubmatch(ctl)
	if signal == nil {
		t.Fatal("scripts/deploy/ocmsctl sends no stop signal; the script changed and this test is vacuous")
	}
	check("scripts/deploy/ocmsctl", time.Duration(waitSec)*time.Second, signal[1])

	if !regexp.MustCompile(`(?m)^\s*exec su-exec ocms "\$@"`).MatchString(read("docker-entrypoint.sh")) {
		t.Error(`docker-entrypoint.sh does not exec su-exec ocms "$@"; without exec the shell stays PID 1 and the stop signal never reaches the app`)
	}
}
