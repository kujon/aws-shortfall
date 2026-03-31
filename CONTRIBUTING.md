# Contributing to AWS EDP Shortfall Calculator

Thank you for your interest in contributing! This document provides guidelines for contributing to the project.

## Development Setup

### Prerequisites

- Go 1.26 or later
- AWS account with appropriate permissions
- Git

### Getting Started

1. Fork the repository
2. Clone your fork:
   ```bash
   git clone https://github.com/YOUR_USERNAME/aws-shortfall.git
   cd aws-shortfall
   ```
3. Install dependencies:
   ```bash
   go mod download
   ```
4. Create a new branch:
   ```bash
   git checkout -b feature/your-feature-name
   ```

## Code Quality Standards

Before submitting a PR, ensure your code passes all quality checks:

### 1. Formatting
```bash
go fmt ./...
goimports -w .
```

### 2. Linting
```bash
go vet ./...
```

### 3. Module Tidiness
```bash
go mod tidy
```

### 4. Build
```bash
go build -o aws-shortfall main.go
```

### 5. Tests
```bash
go test -v -race ./...
```

## Commit Message Convention

Use conventional commits format:

- `feat:` - New features
- `fix:` - Bug fixes
- `docs:` - Documentation changes
- `refactor:` - Code refactoring
- `test:` - Adding or updating tests
- `chore:` - Maintenance tasks
- `ci:` - CI/CD changes

Example:
```
feat: add support for custom date ranges

- Allow users to specify custom EDP period dates
- Update CLI flags for start and end dates
- Add validation for date format
```

## Pull Request Process

1. **Update Documentation**: If your change affects functionality, update the README.md
2. **Add Tests**: New features should include tests
3. **Run Quality Checks**: Ensure all checks pass locally
4. **Update Changelog**: Add a note about your change
5. **Create PR**: 
   - Use a clear, descriptive title
   - Reference any related issues
   - Describe what changed and why
   - Include screenshots for UI changes

## CI/CD Pipeline

All PRs trigger automated checks:

- Code formatting (`gofmt`, `goimports`)
- Static analysis (`go vet`)
- Module tidiness (`go mod tidy`)
- Build verification
- Test execution with race detection

Your PR must pass all checks before it can be merged.

## Testing

### Running Tests
```bash
# All tests
go test ./...

# With coverage
go test -cover ./...

# With race detection
go test -race ./...

# Verbose output
go test -v ./...
```

### Writing Tests
- Place tests in `*_test.go` files
- Use table-driven tests where appropriate
- Mock external dependencies (AWS APIs)
- Test both success and error cases

## Release Process

Releases are automated via GitHub Actions:

1. Update version in code if needed
2. Create a git tag:
   ```bash
   git tag -a v1.0.0 -m "Release v1.0.0"
   git push origin v1.0.0
   ```
3. GitHub Actions will:
   - Build binaries for all platforms
   - Create a GitHub release
   - Upload binaries

## Getting Help

- Open an issue for bug reports or feature requests
- Check existing issues before creating new ones
- Be respectful and constructive in discussions

## Code of Conduct

- Be respectful and inclusive
- Focus on what is best for the community
- Show empathy towards other community members

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
