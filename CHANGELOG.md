# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Planned
- macOS binary support
- Docker image
- Prometheus metrics endpoint

## [1.0.0] - 2026-01-07

### Added
- ✨ Complete Go implementation of cache warmer
- 📊 Dashboard command with colored output
- 🗺️ Sitemap crawler with nested sitemap support
- 💾 SQLite database for state tracking
- ⚡ Concurrent URL warming with goroutines
- 🔄 Retry logic with exponential backoff
- 🎯 Load-aware pausing at high CPU
- 📝 CLI commands: `init`, `status`, `run`, `once`, `flush`
- 🚀 GitHub Actions workflow for automated builds
- 📦 Multi-platform binaries (Linux AMD64/ARM64)
- 🔐 SHA256 checksums for binary verification
- 📄 Automatic install script
- 📖 English documentation
- 🎨 Colored terminal output for better UX

### Changed
- 🔄 Migration from Python to Go
- ⚡ Performance: 10-100x faster than Python version
- 💾 Memory: ~75% less memory usage
- 📦 Deployment: Single binary without dependencies

### Removed
- ❌ Python implementation (cache-warmer.py)
- ❌ pip requirements (aiohttp dependency)

## Project Start

### Python Version (deprecated)
- Original Python implementation with aiohttp
- Features: sitemap parsing, concurrent warming, SQLite DB
- Successfully used in production

---

## Release Types

- **MAJOR**: Breaking changes (v2.0.0)
- **MINOR**: New features, backwards compatible (v1.1.0)
- **PATCH**: Bug fixes (v1.0.1)

## Links

- [Releases](https://github.com/hpowernl/cache-warmer/releases)
- [Issues](https://github.com/hpowernl/cache-warmer/issues)
- [Repository](https://github.com/hpowernl/cache-warmer)
