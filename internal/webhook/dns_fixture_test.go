// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"os"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil/dnsfixture"
	"github.com/olegiv/ocms-go/internal/util"
)

// TestMain resolves the fixture hosts offline: the validation under test
// resolves names, and the sandboxes that run these tests may have no DNS.
func TestMain(m *testing.M) {
	restore := util.ReplaceLookupIPAddrForTest(dnsfixture.Lookup)
	code := m.Run()
	restore()
	os.Exit(code)
}
