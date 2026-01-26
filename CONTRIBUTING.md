# Contributing to oCMS

Thank you for your interest in contributing to oCMS! This document provides guidelines for contributing to the project.

## Code of Conduct

Please be respectful and constructive in all interactions. We welcome contributors of all experience levels.

## How to Contribute

### Reporting Issues

1. Check existing issues to avoid duplicates
2. Use a clear, descriptive title
3. Include steps to reproduce the problem
4. Provide system information (Go version, OS, browser if relevant)
5. Include relevant logs or error messages

### Submitting Pull Requests

1. Fork the repository
2. Create a feature branch from `master`:
   ```bash
   git checkout -b feature/your-feature-name
   ```
3. Make your changes following the coding standards below
4. Write or update tests as needed
5. Ensure all tests pass:
   ```bash
   OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test ./...
   ```
6. Commit with a clear message describing the change
7. Push to your fork and open a pull request

## Development Setup

See the [README.md](README.md) for full setup instructions.

Quick start:
```bash
git clone https://github.com/olegiv/ocms-go.git
cd ocms-go
make assets
OCMS_SESSION_SECRET=your-secret-key-32-bytes make dev
```

## Coding Standards

### Go Code

- Follow standard Go conventions
- Run `go fmt` before committing
- Run `go vet ./...` to check for issues
- Use meaningful variable and function names
- Add comments for exported functions and complex logic

### Testing

- Write tests for new functionality
- Ensure existing tests pass before submitting
- Aim for meaningful test coverage, not just high percentages

### Commit Messages

- Use clear, descriptive commit messages
- Start with a verb in imperative mood (Add, Fix, Update, Remove)
- Keep the first line under 50 characters
- Add details in the body if needed

Example:
```
Add webhook retry mechanism

Implement exponential backoff for failed webhook deliveries.
Maximum 5 retries with delays of 1, 2, 4, 8, 16 minutes.
```

## Database Changes

If your change requires database modifications:

1. Create a migration file:
   ```bash
   make migrate-create name=your_migration_name
   ```
2. Write both up and down migrations
3. Update SQLC queries if needed:
   ```bash
   sqlc generate
   ```

## License

By contributing to oCMS, you agree that your contributions will be licensed under the GPL-3.0 License. All contributions must be compatible with this license.

## Questions?

Open an issue for questions about contributing. We're happy to help!
