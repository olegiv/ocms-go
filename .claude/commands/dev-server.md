Start the oCMS development server with asset compilation.

Steps:
1. Check if OCMS_SESSION_SECRET is set in environment
2. If not set, prompt user to set it (minimum 32 bytes required)
3. Run `make dev` to compile assets and start server
4. Wait for server to start (check for "Server listening on..." message)
5. Report server status and URL (default: http://localhost:8080)
6. Provide useful URLs:
   - Admin: http://localhost:8080/admin
   - API docs: http://localhost:8080/api/v1/docs
   - Health check: http://localhost:8080/health

The `make dev` command will:
- Compile SCSS to CSS
- Start the Go development server with hot reload (if using air/realize)
- Or start with `go run ./cmd/ocms`

To stop the server: Use `make stop` or Ctrl+C

Note: Default credentials on first run:
- Email: admin@example.com
- Password: changeme
