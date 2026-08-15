// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package shared

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"github.com/olegiv/ocms-go/internal/auth"
)

// baseCfg returns a valid source-database configuration.
func baseCfg() map[string]string {
	return map[string]string{
		"mysql_host":     "db.internal",
		"mysql_port":     "3306",
		"mysql_user":     "someuser",
		"mysql_password": "somepass",
		"mysql_database": "sourcedb",
	}
}

// TestBuildMySQLDSNRejectsParameterInjection is the regression test for the
// DSN parameter injection that let a database name such as
// "db?allowAllFiles=true" turn on the driver's LOCAL INFILE handling, giving a
// hostile MySQL server arbitrary local file read on this host.
//
// It fails on the pre-fix state, where the DSN was assembled with fmt.Sprintf.
func TestBuildMySQLDSNRejectsParameterInjection(t *testing.T) {
	injections := []struct {
		name  string
		field string
		value string
	}{
		{"allowAllFiles via database", "mysql_database", "sourcedb?allowAllFiles=true&charset=utf8mb4,x"},
		{"multiStatements via database", "mysql_database", "sourcedb?multiStatements=true"},
		{"interpolateParams via database", "mysql_database", "sourcedb?interpolateParams=true"},
		{"allowAllFiles via user", "mysql_user", "u?allowAllFiles=true"},
		{"allowAllFiles via password", "mysql_password", "p?allowAllFiles=true"},
		{"protocol switch via host", "mysql_host", "x@unix(/var/run/mysqld/mysqld.sock"},
	}

	for _, tc := range injections {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseCfg()
			cfg[tc.field] = tc.value

			dsn, err := BuildMySQLDSN(cfg, MySQLDSNOptions{})
			if err != nil {
				// Rejecting outright is an acceptable outcome.
				return
			}

			parsed, err := mysql.ParseDSN(dsn)
			if err != nil {
				t.Fatalf("BuildMySQLDSN produced an unparseable DSN: %v", err)
			}
			if parsed.AllowAllFiles {
				t.Errorf("AllowAllFiles was injected via %s=%q\nDSN: %s", tc.field, tc.value, dsn)
			}
			if parsed.MultiStatements {
				t.Errorf("MultiStatements was injected via %s=%q\nDSN: %s", tc.field, tc.value, dsn)
			}
			if parsed.InterpolateParams {
				t.Errorf("InterpolateParams was injected via %s=%q\nDSN: %s", tc.field, tc.value, dsn)
			}
			if parsed.Net != "tcp" {
				t.Errorf("Net was switched to %q via %s=%q\nDSN: %s", parsed.Net, tc.field, tc.value, dsn)
			}
		})
	}
}

// TestBuildMySQLDSNEnforcesHostAllowlist proves the allowlist is enforced
// inside the shared builder, so no source can opt out of it.
func TestBuildMySQLDSNEnforcesHostAllowlist(t *testing.T) {
	t.Setenv(EnvAllowedDBHosts, "allowed.example.com")

	cfg := baseCfg()
	cfg["mysql_host"] = "evil.example.com"
	if _, err := BuildMySQLDSN(cfg, MySQLDSNOptions{}); err == nil {
		t.Fatal("BuildMySQLDSN accepted a host outside the allowlist")
	}

	cfg["mysql_host"] = "allowed.example.com"
	if _, err := BuildMySQLDSN(cfg, MySQLDSNOptions{}); err != nil {
		t.Fatalf("BuildMySQLDSN rejected an allowlisted host: %v", err)
	}
}

// TestBuildMySQLDSNJoinsIPv6HostsCorrectly guards the host-canonicalization
// fix. Two distinct bugs live here:
//
//   - "::1" without net.JoinHostPort produced the unparseable "::1:3306".
//   - "[::1]" — the form an admin copies out of a connection string — passed
//     the allowlist (normalizeHost strips brackets) and was then dialed raw,
//     so JoinHostPort re-bracketed it into "[[[::1]]:3306]:3306".
//
// The second is why the allowlist and the dial target must be the same
// canonical string. Testing only the bare "::1" passed on that bug.
func TestBuildMySQLDSNJoinsIPv6HostsCorrectly(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"::1", "[::1]:3306"},
		{"[::1]", "[::1]:3306"},
		{"2001:db8::1", "[2001:db8::1]:3306"},
		{"[2001:db8::1]", "[2001:db8::1]:3306"},
		{"0:0:0:0:0:0:0:1", "[::1]:3306"},
		{"localhost", "localhost:3306"},
		{"db.example.com.", "db.example.com:3306"},
		{"DB.Example.COM", "db.example.com:3306"},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			cfg := baseCfg()
			cfg["mysql_host"] = tc.host

			dsn, err := BuildMySQLDSN(cfg, MySQLDSNOptions{})
			if err != nil {
				t.Fatalf("BuildMySQLDSN(%q) returned an error: %v", tc.host, err)
			}
			parsed, err := mysql.ParseDSN(dsn)
			if err != nil {
				t.Fatalf("BuildMySQLDSN(%q) produced an unparseable DSN %q: %v", tc.host, dsn, err)
			}
			if parsed.Addr != tc.want {
				t.Errorf("host %q: Addr = %q, want %q", tc.host, parsed.Addr, tc.want)
			}
		})
	}
}

// TestBuildMySQLDSNAlwaysSetsTimeouts ensures no source can produce an
// unbounded connection, which previously let a black-holed host pin a
// goroutine and socket for the OS TCP timeout.
func TestBuildMySQLDSNAlwaysSetsTimeouts(t *testing.T) {
	dsn, err := BuildMySQLDSN(baseCfg(), MySQLDSNOptions{})
	if err != nil {
		t.Fatalf("BuildMySQLDSN returned an error: %v", err)
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("unparseable DSN: %v", err)
	}
	if parsed.Timeout <= 0 {
		t.Error("connect Timeout is not set")
	}
	if parsed.ReadTimeout <= 0 {
		t.Error("ReadTimeout is not set")
	}
}

// sourcesDir walks up to the migrator sources tree.
func sourcesDir(t *testing.T) string {
	t.Helper()
	// This test file lives in .../modules/migrator/sources/shared.
	abs, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving sources dir: %v", err)
	}
	return abs
}

// walkSourceFiles calls fn for every non-test Go file under the sources tree.
func walkSourceFiles(t *testing.T, fn func(rel string, file *ast.File, fset *token.FileSet)) {
	t.Helper()
	root := sourcesDir(t)
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil //nolint:nilerr // unparseable files are the compiler's problem
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		fn(rel, parsed, fset)
		return nil
	})
	if err != nil {
		t.Fatalf("walking sources: %v", err)
	}
}

// TestNoSourceBuildsDSNByStringFormatting fails if any migrator source goes
// back to assembling a MySQL DSN with string formatting instead of
// BuildMySQLDSN. That is what made parameter injection possible, and a new
// source could reintroduce it without touching any existing file.
func TestNoSourceBuildsDSNByStringFormatting(t *testing.T) {
	var offenders []string

	walkSourceFiles(t, func(rel string, file *ast.File, fset *token.FileSet) {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			// "@tcp(" only ever appears inside a hand-built MySQL DSN.
			if strings.Contains(value, "@tcp(") {
				offenders = append(offenders, rel+":"+
					strconv.Itoa(fset.Position(lit.Pos()).Line)+"  "+lit.Value)
			}
			return true
		})
	})

	if len(offenders) > 0 {
		t.Errorf("migrator sources must build DSNs via shared.BuildMySQLDSN, "+
			"which escapes parameters and enforces %s.\nHand-built DSNs found:\n  %s",
			EnvAllowedDBHosts, strings.Join(offenders, "\n  "))
	}
}

// TestSourceReadersUseContextAwareQueries fails on any database call that
// cannot be cancelled. A migration runs for up to six hours in a detached
// goroutine; a query without a context ignores both shutdown and the job
// deadline, and a Ping without one blocks for the OS TCP timeout.
// dbHandleNames are the identifiers a *sql.DB / *sql.Tx is held under in this
// module, as a field or as a local. Name-based matching is a deliberate
// trade-off: it keeps the test dependency-free, at the cost of missing a handle
// stored under an unconventional name.
var dbHandleNames = map[string]bool{
	"db": true, "conn": true, "tx": true, "sqlDB": true,
}

func TestSourceReadersUseContextAwareQueries(t *testing.T) {
	// Non-context database calls on *sql.DB / *sql.Tx.
	banned := map[string]string{
		"Query":    "QueryContext",
		"QueryRow": "QueryRowContext",
		"Exec":     "ExecContext",
		"Ping":     "PingContext",
		"Prepare":  "PrepareContext",
		"Begin":    "BeginTx",
	}

	var offenders []string

	walkSourceFiles(t, func(rel string, file *ast.File, fset *token.FileSet) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			want, isBanned := banned[sel.Sel.Name]
			if !isBanned {
				return true
			}
			// Match a DB handle whether it is a field (r.db.Query) or a local
			// variable (db.Ping, as in the pre-fix NewReader). Requiring a
			// selector receiver made this test blind to the local-variable
			// form — which is the exact shape of the bug it was written for.
			var recvName string
			switch recv := sel.X.(type) {
			case *ast.SelectorExpr:
				recvName = recv.Sel.Name
			case *ast.Ident:
				recvName = recv.Name
			default:
				return true
			}
			if !dbHandleNames[recvName] {
				return true
			}
			offenders = append(offenders, rel+":"+
				strconv.Itoa(fset.Position(call.Pos()).Line)+"  ."+sel.Sel.Name+"() should be ."+want+"()")
			return true
		})
	})

	if len(offenders) > 0 {
		t.Errorf("migrator source database calls must be context-aware:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestSanitizeIdentifier covers the helper that guards every interpolated SQL
// identifier. It had no tests at all, which is a poor state for the one
// function standing between a column name and a query string.
func TestSanitizeIdentifier(t *testing.T) {
	valid := []string{
		"field_image_target_id",
		"entity_id",
		"NodeID",
		"a",
		"_leading_underscore",
		"col9",
		strings.Repeat("x", MaxIdentifierLength),
	}
	for _, name := range valid {
		got, err := SanitizeIdentifier(name)
		if err != nil {
			t.Errorf("SanitizeIdentifier(%q) returned an error: %v", name, err)
			continue
		}
		if got != name {
			t.Errorf("SanitizeIdentifier(%q) = %q, want the input unchanged", name, got)
		}
	}

	invalid := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"backtick escape", "id` FROM users WHERE 1=1 -- "},
		{"quote", "id'"},
		{"semicolon", "id;DROP TABLE x"},
		{"space", "id name"},
		{"parenthesis", "count(*)"},
		{"comma", "id,name"},
		{"hyphen", "id-name"},
		{"dot qualified", "t.id"},
		{"asterisk", "*"},
		{"newline", "id\nname"},
		{"null byte", "id\x00"},
		{"unicode homoglyph", "іd"},
		{"too long", strings.Repeat("x", MaxIdentifierLength+1)},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := SanitizeIdentifier(tc.input); err == nil {
				t.Errorf("SanitizeIdentifier(%q) = %q, want an error", tc.input, got)
			}
		})
	}
}

// TestPlaceholderHashIsUnguessable proves the imported-account placeholder is
// not derived from anything in the source tree.
func TestPlaceholderHashIsUnguessable(t *testing.T) {
	first, err := UnguessablePlaceholderHash()
	if err != nil {
		t.Fatalf("UnguessablePlaceholderHash: %v", err)
	}
	second, err := UnguessablePlaceholderHash()
	if err != nil {
		t.Fatalf("UnguessablePlaceholderHash: %v", err)
	}
	if first == "" {
		t.Fatal("empty hash: imported accounts would have no credential at all")
	}
	if first == second {
		t.Error("two calls produced the same hash; the secret is not random")
	}
	// The string this replaced, plus the obvious variants.
	for _, guess := range []string{"imported-user-must-reset", "", "password", "changeme"} {
		if ok, _ := auth.CheckPassword(guess, first); ok {
			t.Errorf("placeholder hash verifies against the known string %q", guess)
		}
	}
}

// TestNoSourceHashesAConstantCredential fails if any source goes back to
// hashing a literal as an account password.
//
// The Drupal and Elefant sources both shipped hashing the constant
// "imported-user-must-reset", which put the plaintext in the repository and let
// anyone sign in as any imported user. A new source could reintroduce that
// without touching either existing file.
func TestNoSourceHashesAConstantCredential(t *testing.T) {
	hashCalls := map[string]bool{
		"HashPassword":         true,
		"GenerateFromPassword": true,
	}

	var offenders []string

	walkSourceFiles(t, func(rel string, file *ast.File, fset *token.FileSet) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !hashCalls[sel.Sel.Name] {
				return true
			}
			// Flag a string literal argument, including one wrapped in a
			// conversion such as []byte("...").
			arg := call.Args[0]
			if conv, isConv := arg.(*ast.CallExpr); isConv && len(conv.Args) == 1 {
				arg = conv.Args[0]
			}
			if lit, isLit := arg.(*ast.BasicLit); isLit && lit.Kind == token.STRING {
				offenders = append(offenders, rel+":"+
					strconv.Itoa(fset.Position(call.Pos()).Line)+"  "+sel.Sel.Name+"("+lit.Value+")")
			}
			return true
		})
	})

	if len(offenders) > 0 {
		t.Errorf("imported-account credentials must be random, never a literal in the "+
			"source tree — use shared.UnguessablePlaceholderHash:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestBuildMySQLDSNRejectsHostWithPort covers the misconfiguration the shape
// check used to wave through.
//
// "db.example.com:3306" passed validation, then net.JoinHostPort produced
// "[db.example.com:3306]:3306" — an address that fails to dial with an error
// that says nothing about the real problem. IPv6 literals must still work.
func TestBuildMySQLDSNRejectsHostWithPort(t *testing.T) {
	for _, host := range []string{"db.example.com:3306", "localhost:3306", "10.0.0.5:3306"} {
		cfg := baseCfg()
		cfg["mysql_host"] = host
		if _, err := BuildMySQLDSN(cfg, MySQLDSNOptions{}); err == nil {
			t.Errorf("BuildMySQLDSN accepted host %q, which carries a port", host)
		}
	}
	// Not a regression for IPv6, bracketed or bare.
	for _, host := range []string{"::1", "[::1]", "2001:db8::1"} {
		cfg := baseCfg()
		cfg["mysql_host"] = host
		if _, err := BuildMySQLDSN(cfg, MySQLDSNOptions{}); err != nil {
			t.Errorf("BuildMySQLDSN rejected the IPv6 literal %q: %v", host, err)
		}
	}
}
