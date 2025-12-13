Scan the project for security vulnerabilities using govulncheck.

Steps:
1. Run `govulncheck ./...` to scan all packages
2. Analyze the results:
   - Count total vulnerabilities found
   - Categorize by severity (if available)
   - Identify affected packages and versions
3. For each vulnerability found:
   - Explain what it is
   - Assess if it affects this application
   - Suggest remediation (version upgrade, patch, workaround)
4. Save audit report to `.audit/YYYY-MM-DD-vulnerability-scan.md`
5. Provide summary of findings and recommended actions

If vulnerabilities are found:
- List affected dependencies
- Suggest specific version upgrades
- Provide commands to update: `go get package@version`

If no vulnerabilities found:
- Confirm all dependencies are secure
- Note the scan date for future reference

Additional security checks:
- Verify OCMS_SESSION_SECRET is set and >= 32 bytes
- Check for hardcoded secrets in code
- Review authentication middleware
- Verify CSRF protection is enabled

Note: The `.audit/` directory is gitignored and stores local security audit results.
