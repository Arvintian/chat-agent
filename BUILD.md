# Build and Release Guide

This document describes how to build and release the chat-agent CLI for multiple platforms.

## Build System Overview

The project uses a Makefile-based build system with support for:
- Cross-compilation for Linux, macOS, and Windows
- ARM64 (Apple Silicon) and x86_64 architectures
- Automated release creation with GitHub Actions
- Version management with git tags

## Quick Start

### Building for Development
```bash
# Build for your current platform
make build

# Run the binary
./dist/chat-agent --version
```

### Building for All Platforms
```bash
# Build binaries for all supported platforms
make build-all

# Create release archives (.tar.gz/.zip)
make release
```

## Supported Platforms

| Platform | Architecture | Binary Name | Archive |
|----------|--------------|-------------|---------|
| Linux | x86_64 (amd64) | `chat-agent-linux-amd64` | `.tar.gz` |
| Linux | ARM64 | `chat-agent-linux-arm64` | `.tar.gz` |
| macOS | x86_64 (amd64) | `chat-agent-darwin-amd64` | `.tar.gz` |
| macOS | ARM64 (Apple Silicon) | `chat-agent-darwin-arm64` | `.tar.gz` |
| Windows | x86_64 (amd64) | `chat-agent-windows-amd64.exe` | `.zip` |

## Release Process

### Automated Release (Recommended)
1. Create a git tag:
   ```bash
   git tag -a v1.0.0 -m "Release v1.0.0"
   git push origin v1.0.0
   ```

2. GitHub Actions will automatically:
   - Build binaries for all platforms
   - Create release archives
   - Publish to GitHub Releases

### Manual Release
1. Build all binaries:
   ```bash
   make build-all
   ```

2. Create release archives:
   ```bash
   make release
   ```

3. Manually upload the files from `dist/` directory to your release.

## Version Management

### Version Information
The binary includes version information from git:
- `--version` flag shows git tag, build time, and Go version
- Version is embedded during build using `-ldflags`

### Checking Version
```bash
# Check binary version
./dist/chat-agent --version

# Check git version info
./scripts/version.sh
```

## GitHub Actions Workflows

Two workflows are configured:

### 1. Build and Test Workflow (`build.yml`)
- Runs on push to main and pull requests
- Runs tests and linter on Linux
- Verifies builds for all platforms (cross-compilation)
- No artifact upload - focused on validation

### 2. Release Workflow (`release.yml`)
- Triggers on git tag push (e.g., `v*`)
- Runs tests first to ensure quality
- Builds binaries for all platforms
- Creates release archives
- Publishes to GitHub Releases

## Scripts

### `scripts/release.sh`
Interactive release script that:
- Checks for uncommitted changes
- Prompts for version bump type (major/minor/patch/custom)
- Runs tests
- Builds all binaries
- Creates git tag
- Pushes to remote

### `scripts/version.sh`
Displays version information:
- Git version and commit hash
- Go version
- Binary information (if built)
- Available binaries in dist/
- Recent git tags

## Development Commands

```bash
# Run tests
make test

# Run linter
make lint

# Clean build artifacts
make clean

# Install to system
make install
```

## Troubleshooting

### Build Issues
- Ensure Go 1.25+ is installed
- Check `go mod tidy` has been run
- Verify all dependencies are available

### Cross-Compilation Issues
- Go cross-compilation requires CGO_ENABLED=0 for some platforms
- Windows builds may require additional setup for CGO

### Release Issues
- Ensure git tags follow semantic versioning (vX.Y.Z)
- Check GitHub Actions permissions for repository
- Verify all required binaries are built before release