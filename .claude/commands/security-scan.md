Scan the project for security vulnerabilities using govulncheck and npm audit.

Steps:
1. Run `govulncheck ./...` to scan Go packages
2. Run `npm audit` to scan npm packages (htmx, alpine.js)
3. Analyze the results:
   - Count total vulnerabilities found
   - Categorize by severity (if available)
   - Identify affected packages and versions
4. For each vulnerability found:
   - Explain what it is
   - Assess if it affects this application
   - Suggest remediation (version upgrade, patch, workaround)
5. Save audit report to `.audit/YYYY-MM-DD-vulnerability-scan.md`
6. Provide summary of findings and recommended actions

If vulnerabilities are found:
- List affected dependencies (Go and npm)
- Suggest specific version upgrades
- Provide commands to update:
  - Go: `go get package@version`
  - npm: `npm update` or edit `package.json`

If no vulnerabilities found:
- Confirm all dependencies are secure
- Note the scan date for future reference

Additional security checks:
- Verify OCMS_SESSION_SECRET is set and >= 32 bytes
- Check for hardcoded secrets in code
- Review authentication middleware
- Verify CSRF protection is enabled

Note: The `.audit/` directory is gitignored and stores local security audit results.
