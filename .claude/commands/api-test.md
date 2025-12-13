Test REST API endpoints with actual HTTP requests.

Steps:
1. Start the server in background:
   `OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go run ./cmd/ocms &`
2. Wait 3 seconds for server startup
3. Test public endpoints:
   - Health check: `curl -s http://localhost:8080/health`
   - API docs: `curl -s http://localhost:8080/api/v1/docs`
   - Public pages: `curl -s http://localhost:8080/api/v1/pages?published=true`
   - Tags: `curl -s http://localhost:8080/api/v1/tags`
   - Categories: `curl -s http://localhost:8080/api/v1/categories`
4. Report response status codes and any errors
5. Stop the server: `pkill -f "go run ./cmd/ocms" || true`

For authenticated API testing (requires API key):
```bash
curl -H "Authorization: Bearer YOUR_API_KEY" \
     http://localhost:8080/api/v1/pages
```

API keys can be created via admin UI at: /admin/api-keys

Expected responses:
- 200 OK for successful requests
- 401 Unauthorized if API key missing/invalid
- 403 Forbidden if insufficient permissions
- 404 Not Found if resource doesn't exist
