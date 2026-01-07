# Changelog

All notable changes to this project will be documented in this file.


## [1.0.1] - 2026-01-07

### Added
- 🖥️ Auto-detect CPU count for `max_load` configuration
  - `init` command now automatically sets `max_load = CPU_COUNT - 1`
  - Examples: 3 CPU = 2.0, 4 CPU = 3.0, 12 CPU = 11.0
- ✅ GitHub releases now automatically marked as "latest"
- 📝 Shows detected CPU count when creating config

### Changed
- 📂 Install directory corrected to `/data/web/cache-warmer`
- 🛠️ ARM64 cross-compilation now includes required C libraries (`libc6-dev-arm64-cross`)
- 🧪 ARM64 binaries are now tested with QEMU before release
- 📖 Simplified README with focus on production setup

### Fixed
- 🐛 GitHub Actions release creation permissions (added `contents: write`)
- 🔧 Install script now creates install directory if it doesn't exist
- 🔄 Install script fallback to tags if no release exists

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

---

## Release Types

- **MAJOR**: Breaking changes (v2.0.0)
- **MINOR**: New features, backwards compatible (v1.1.0)
- **PATCH**: Bug fixes (v1.0.1)

## Links

- [Releases](https://github.com/hpowernl/cache-warmer/releases)
- [Issues](https://github.com/hpowernl/cache-warmer/issues)
- [Repository](https://github.com/hpowernl/cache-warmer)
