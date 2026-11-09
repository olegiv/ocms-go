# Shared development authenticator

`ocms-dev-auth` generates standard oCMS MFA codes for a dedicated development
account. Codex and Claude Code use the same executable, macOS login Keychain
item and per-account code reservation. It requires no provider account, network
service, new dependency or application deployment.

## Availability and activation

Build from this checkout:

```sh
make build-dev-authenticator
bin/ocms-dev-auth status --profile mcp-staging --account ocms-agent@example.test
```

The staging profile is bound to `https://ocms-mcp-staging.fly.dev`. For a local
instance, use `--profile local --origin http://127.0.0.1:18080` with its own
account. Local origins must use an IP-literal loopback address. Production
profiles and arbitrary remote origins are rejected.

**Active on staging since 2026-11-01:** `ocms-agent@example.test`, user ID 2,
Editor role. A one-off maintenance command used the existing password hashing,
user store and MFA enrollment service; it did not change the running image.
Normal browser password/MFA login subsequently passed with an automatically
generated code, and Account > Security shows MFA enabled with ten recovery codes.
The owner's account and authenticator were not changed.

The account password and recovery codes are also retained in the login Keychain,
under services `tech.ocms.dev-auth.password.v1` and
`tech.ocms.dev-auth.recovery.v1`. Their account identifier matches the seed item:
SHA256 of the exact origin, a newline, and the account email. Values are base64
JSON records bound to that origin/account. Load the password privately into the
browser controller; never print `security find-generic-password -w` output.
Both agents can use the same stored credentials and the helper's `code` command.

For a new development instance/account, use this enrollment workflow:

1. Use the normal admin UI to create a dedicated staging account with the
   minimum role required for the remaining MCP tests. This proposed account is
   separate from the owner's existing authenticator and working connections.
2. Sign in as that account and start normal authenticator enrollment. Read
   only its newly displayed manual setup seed (or enrollment `otpauth` URI)
   privately in the browser controller. Do not print or screenshot it.
3. Pipe that value directly into the command below. Use a private child-process
   stdin pipe, or a FIFO with mode `0600` inside a temporary `0700` directory
   outside the checkout. Never put the seed in arguments, environment variables,
   clipboard, chat, shell history, screenshots or regular files.
4. Request a code with `code`, fill the enrollment form from memory, submit it
   once and retain recovery codes privately. A subsequent login requests a
   fresh code through the same helper; the owner does not type OTPs.

```sh
# stdin must come from the private enrollment pipe described above.
bin/ocms-dev-auth import --profile mcp-staging --account ocms-agent@example.test
```

Import accepts a base32 seed or a matching-account TOTP URI, restricted to SHA1,
six digits and a 30-second period. It refuses an existing enrollment. It does
not export seeds, replace authenticators or reset MFA. Existing personal seeds
must not be extracted from the server or password manager to activate this tool.

## Agent login workflow

Both agents follow these steps for each normal development login:

1. Check `status` for the exact target account and origin. If it reports
   `configured: false`, enrollment is pending; do not ask for repeated manual
   codes or treat a missing seed as an invalid-code failure.
2. Open a fresh login/consent flow and reach its MFA field before issuing a
   code. Serialize sign-ins that share browser cookies.
3. Run:

   ```sh
   bin/ocms-dev-auth code --profile mcp-staging --account ocms-agent@example.test
   ```

4. Capture the JSON metadata (`code_file`, `origin`, `account`, `expires_at`).
   Read the six digits from `code_file` into the browser controller's memory.
   Check the origin/account and expiration, replace the field contents once,
   and click Verify once using the controller's supported UI API. Never log
   the file contents, include them in a tool argument or capture the filled
   field in a screenshot. Remove the temporary file and its directory in a
   `finally` block, including on browser failure.
5. Complete the normal scope consent. If the login expired, start a fresh flow
   and request another code. A failed delivery also reserves its time step;
   do not reuse an older file or replay a code.

For MCP acceptance, serialize scratch write/lifecycle tests for this account.
The probe retains one entry per user; a later save from another client replaces
the earlier ID. OAuth reconnect and bearer restoration can read the latest entry
across grants/keys, provided that no intervening save replaced it.

`code` prints only metadata; the actual code is delivered in a new `0600` file
inside a `0700` temporary directory. It waits when fewer than ten seconds remain
or another agent already reserved that 30-second window. A shared OS file lock
and nonsecret last-step record under the user cache coordinate separate
processes. The command has a 55-second deadline; clock rollback fails clearly.
The server retains its independent replay checks, rate limits and required MFA.

The consumer's `finally` block remains the primary cleanup of the code file; the
command adds safety nets for when it never runs. Interrupt, SIGTERM and SIGHUP
cancel the command, which then removes a code file it has not yet reported (a
second signal terminates it immediately). Each `code` run also deletes
`ocms-dev-auth-*` directories left in the temporary directory for more than two
minutes. The sweep is best effort and silent, and removes only directories this
tool creates: exact generated name, not a symlink, mode `0700`, owned by the
current user and holding at most the `code` file. The last-step record is
replaced atomically (temporary file in the same directory, fsync, rename), so a
crash cannot leave an empty record that would block later runs.

The seed and origin/account binding are stored only in the macOS login
Keychain under service `tech.ocms.dev-auth.v1`, with a hashed account identifier.
Both agents must run as the same macOS user. Keychain writes use `security`'s
private stdin, without secret process arguments, changes to the default
Keychain or an allow-all-applications ACL. The login Keychain and this macOS account are the trust boundary.
The `security` utility can read the enrolled item on behalf of another
process running as the same user while the Keychain is unlocked. Treat
the shared development account as accessible to that user's agents and
other processes. Use a dedicated macOS account for the agents when
local processes need to be isolated from the development enrollment.
The helper requires an available, unlocked login Keychain. Linux builds
can run the fixture tests; live seed storage currently requires macOS.

## Verification

```sh
go test -race ./cmd/ocms-dev-auth
golangci-lint run ./cmd/ocms-dev-auth/...
# Optional: public RFC seed only; creates and removes one disposable item.
OCMS_DEV_AUTH_KEYCHAIN_TEST=1 go test -count=1 -run '^TestLoginKeychainRoundTrip$' -v ./cmd/ocms-dev-auth
```

Tests cover RFC vectors, rejected parameters/targets, account binding, private
delivery, shared reservations, cancellation, shutdown signals (subprocess tests),
the stale-directory sweep, atomic reservation replacement, and a real disposable oCMS database
enrollment/verification with replay rejection. The opt-in check exercises the
actual login Keychain and CLI stdin-to-file flow. These checks do not establish
the Claude Code OAuth lifecycle. The separately recorded live staging browser
login does establish activation of the dedicated account's shared authenticator.
