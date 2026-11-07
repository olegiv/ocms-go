For this proxied audit, the launcher has already archived previous reports.
Perform the audit described by the command directly in this session using
Read, Glob, Grep and the available shell checks. The launcher saves your final
Markdown response as the report. Include findings, evidence, risk, suggested
remediation and any checks that could not run. Previous reports in the archive
directories are available for comparison.

Use these dependency checks when their tools and inputs are available:

```bash
govulncheck ./...
govulncheck -json ./...
npm audit
npm audit --json
npm outdated
```

Directory creation, archive moves, report writes, and launching the audit
agent are handled by this workflow or require the native interactive command.
Dependency upgrades and any other shell checks requiring extra permissions
are manual. Report those as follow-ups with the exact checks that were skipped.
