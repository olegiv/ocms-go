---
description: "Run all Go tests for the oCMS project with verbose output and coverage reporting"
---

Run all Go tests for the oCMS project with verbose output and coverage reporting.

Steps:
1. Run the uncached suite with the development test secret and coverage enabled.

   ```bash
   OCMS_SESSION_SECRET='test-secret-key-32-bytes-long!!!' go test -count=1 -v -cover ./...
   ```

2. Report test results including:
   - Number of tests passed/failed
   - Test coverage percentage
   - Any test failures with details
3. If tests fail, provide analysis and suggested fixes
