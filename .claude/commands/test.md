Run all Go tests for the oCMS project with verbose output and coverage reporting.

Steps:
1. Set the required OCMS_SESSION_SECRET environment variable
2. Run `go test -v -cover ./...` to execute all tests
3. Report test results including:
   - Number of tests passed/failed
   - Test coverage percentage
   - Any test failures with details
4. If tests fail, provide analysis and suggested fixes

Note: All tests require OCMS_SESSION_SECRET environment variable to be set.
