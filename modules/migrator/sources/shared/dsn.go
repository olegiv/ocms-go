// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package shared

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Default connection bounds applied to every source database DSN. A migration
// is a long sequence of modest reads, so statements stay bounded even when a
// source forgets to tune them.
const (
	DefaultConnectTimeout = 10 * time.Second
	DefaultReadTimeout    = 60 * time.Second
)

// MySQLDSNOptions tunes the timeouts baked into the generated DSN. The zero
// value selects the defaults above.
type MySQLDSNOptions struct {
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
}

// BuildMySQLDSN assembles a MySQL DSN from submitted admin form configuration.
//
// Every migrator source MUST build its DSN through this helper. It exists
// because assembling a DSN with fmt.Sprintf is unsafe: the driver parses
// everything after the database name as connection parameters, so a database
// name of "db?allowAllFiles=true" turns on the driver's LOCAL INFILE handling
// and lets a hostile MySQL server read arbitrary files off this host. Routing
// through mysql.Config.FormatDSN escapes DBName and parameter values, so a
// stray '?', '&', '@', '/' or ':' in any field is data rather than syntax.
//
// It is also the single choke point where CheckDBHostAllowed is enforced, so a
// source cannot silently opt out of OCMS_MIGRATOR_ALLOWED_DB_HOSTS.
func BuildMySQLDSN(cfg map[string]string, opts MySQLDSNOptions) (string, error) {
	port := strings.TrimSpace(cfg["mysql_port"])
	if port == "" {
		port = "3306"
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return "", fmt.Errorf("invalid MySQL port %q", cfg["mysql_port"])
	}

	host := strings.TrimSpace(cfg["mysql_host"])
	if host == "" {
		return "", fmt.Errorf("MySQL host is required")
	}
	if err := CheckDBHostAllowed(host); err != nil {
		return "", err
	}

	database := strings.TrimSpace(cfg["mysql_database"])
	if database == "" {
		return "", fmt.Errorf("MySQL database name is required")
	}

	connectTimeout := opts.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = DefaultConnectTimeout
	}
	readTimeout := opts.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = DefaultReadTimeout
	}

	mycfg := mysql.NewConfig()
	mycfg.User = cfg["mysql_user"]
	mycfg.Passwd = cfg["mysql_password"]
	mycfg.Net = "tcp"
	// net.JoinHostPort, not fmt.Sprintf: an IPv6 literal such as "::1" passes
	// the allowlist (normalizeHost strips brackets) and would otherwise dial
	// the unparseable address "::1:3306".
	mycfg.Addr = net.JoinHostPort(host, strconv.Itoa(portNum))
	mycfg.DBName = database
	mycfg.ParseTime = true
	mycfg.Loc = time.UTC
	mycfg.Timeout = connectTimeout
	mycfg.ReadTimeout = readTimeout
	mycfg.AllowNativePasswords = true
	mycfg.Params = map[string]string{"charset": "utf8mb4"}

	return mycfg.FormatDSN(), nil
}
