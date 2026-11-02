// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/olegiv/ocms-go/internal/scheduler"
)

func startCoreScheduler(s *scheduler.Scheduler) error {
	if err := s.AddOAuthCleanup(); err != nil {
		return fmt.Errorf("registering OAuth retention: %w", err)
	}
	return s.Start()
}
