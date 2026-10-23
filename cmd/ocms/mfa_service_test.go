// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
)

// newMFAServiceChildMarker proves a child process ran the scenario.
const newMFAServiceChildMarker = "newMFAService scenario verified"

// TestNewMFAService checks the two-factor service main builds: a requirement
// without OCMS_MFA_KEY is refused, a key with the requirement enables both,
// and demo mode turns two-factor authentication off whatever the
// configuration says. Demo mode is cached globally, so each mode runs in a
// fresh test process.
func TestNewMFAService(t *testing.T) {
	const key = "Zp8Lq3Wn6Rx1Tc9Vb4Ym7Kd2Hf5Gj0Sa8Ne3Ux6Qi="
	switch os.Getenv("OCMS_MFA_SERVICE_CHILD") {
	case "":
		for _, demo := range []string{"false", "true"} {
			command := exec.Command(os.Args[0], "-test.run=^TestNewMFAService$", "-test.v")
			command.Env = append(os.Environ(), "OCMS_DEMO_MODE="+demo, "OCMS_MFA_SERVICE_CHILD="+demo)
			out, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(out), newMFAServiceChildMarker) {
				t.Fatalf("demo mode %s: %v\n%s", demo, err, out)
			}
		}
		return
	case "false":
		if _, err := newMFAService(nil, &config.Config{RequireMFA: true}); err == nil {
			t.Error("a requirement without OCMS_MFA_KEY was accepted")
		}
		svc, err := newMFAService(nil, &config.Config{RequireMFA: true, MFAKey: key})
		if err != nil || !svc.Required() || !svc.Available() {
			t.Errorf("required with a key = %v, required %t, available %t", err, svc != nil && svc.Required(), svc != nil && svc.Available())
		}
	case "true":
		svc, err := newMFAService(nil, &config.Config{RequireMFA: true, MFAKey: key})
		if err != nil || svc.Required() || svc.Available() {
			t.Errorf("demo mode = %v, required %t, available %t; want two-factor off", err, svc != nil && svc.Required(), svc != nil && svc.Available())
		}
	}
	t.Log(newMFAServiceChildMarker)
}
