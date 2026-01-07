# Cache Warmer (Go)

A fast, native cache warmer written in Go for automatically warming caches via sitemap URLs.

## ✨ Features

- 🚀 **Native Binary**: Single executable, no runtime dependencies
- ⚡ **Fast**: Concurrent URL warming with goroutines
- 📊 **Dashboard**: Real-time status overview
- 💾 **State Tracking**: SQLite database for URL status
- 🔄 **Auto-retry**: Retry logic with exponential backoff
- 🎯 **Load-aware**: Pauses during high CPU load
- 🗺️ **Sitemap Support**: Including nested sitemaps and .gz compression
- ⚙️ **Configurable**: TOML configuration file
- 📈 **Cache Flush Tracking**: Mark cache flushes for re-warming

## 📦 Installation

### Option 1: Automatic Install Script (Recommended) ⭐

```bash
# Download and run the install script
curl -sSL https://raw.githubusercontent.com/hpowernl/cache-warmer/main/install.sh | bash

# Or download first and inspect:
curl -O https://raw.githubusercontent.com/hpowernl/cache-warmer/main/install.sh
chmod +x install.sh
./install.sh
```

The script will:
- ✅ Automatically detect your platform (Linux AMD64/ARM64)
- ✅ Download the latest release
- ✅ Install to the configured directory
- ✅ Make it immediately usable

### Option 2: Manual Download from GitHub Releases

1. **Go to the [Releases page](https://github.com/hpowernl/cache-warmer/releases/latest)**

2. **Download the binary for your platform:**
   - Linux x86_64: `cache-warmer-linux-amd64`
   - Linux ARM64: `cache-warmer-linux-arm64`

3. **Install:**
   ```bash
   # Download (replace VERSION with the tag, e.g. v1.0.0)
   wget https://github.com/hpowernl/cache-warmer/releases/download/VERSION/cache-warmer-linux-amd64
   
   # Make executable
   chmod +x cache-warmer-linux-amd64
   
   # Move to PATH (optional)
   sudo mv cache-warmer-linux-amd64 /usr/local/bin/cache-warmer
   
   # Test
   cache-warmer --help
   ```

4. **Verify checksum (recommended):**
   ```bash
   # Download checksums
   wget https://github.com/hpowernl/cache-warmer/releases/download/VERSION/checksums.txt
   
   # Verify
   sha256sum -c checksums.txt
   ```

### Option 3: Build from Source

```bash
# Ensure Go 1.21+ is installed
go version

# Clone the project
git clone https://github.com/hpowernl/cache-warmer.git
cd cache-warmer

# Download dependencies
go mod download

# Build
CGO_ENABLED=1 go build -ldflags="-s -w" -o cache-warmer cache-warmer.go

# Install (optional)
sudo mv cache-warmer /usr/local/bin/
```

## 🚀 Usage

### 1. Initialize

```bash
# Create a config.toml
./cache-warmer init
```

This creates a `config.toml` file with default settings.

### 2. Configure

Edit `config.toml` to your needs:

```toml
[app]
db_path = "warmer.db"
log_file = "logs/cache_warmer.log"
log_level = "INFO"
rewarm_after_hours = 24
loop = true
loop_interval_seconds = 900

[http]
user_agent = "CacheWarmer/1.0 (+cachewarmer)"
timeout_seconds = 20
connect_timeout_seconds = 10
max_redirects = 5
concurrency = 8
min_delay_ms = 50
retries = 2
retry_backoff_seconds = 1.0

[load]
max_load = 2.0
check_interval_seconds = 2

[sitemaps]
urls = [
  "https://www.example.com/sitemap.xml"
]
```

### 3. Check Status

```bash
./cache-warmer status

# Show more URLs
./cache-warmer status --recent 20 --failed 15
```

Example output:
```
======================================================================
   CACHE WARMER DASHBOARD
======================================================================

📊 STATISTICS
----------------------------------------------------------------------
  Total URLs Warmed:    1247
  Successful (2xx-3xx): 1198
  Failed (4xx-5xx):     49
  Last Cache Flush:     2026-01-07T14:23:11Z

✅ RECENTLY WARMED (10 most recent)
----------------------------------------------------------------------
  ✅ [200] 2026-01-07 15:34:22 | https://example.com/page1
  ✅ [200] 2026-01-07 15:34:21 | https://example.com/page2
  ...

❌ RECENT FAILURES (10 most recent)
----------------------------------------------------------------------
  ❌ [404] 2026-01-07 15:20:11
     URL: https://example.com/old-page
     Error: HTTP 404
  ...
```

### 4. Run Commands

```bash
# Run once (for testing)
./cache-warmer once

# Continuous loop (production)
./cache-warmer run

# With custom config
./cache-warmer run --config /path/to/config.toml
```

### 5. Mark Cache Flush

```bash
# Simple (uses "manual flush" as reason)
./cache-warmer flush

# With custom reason
./cache-warmer flush --reason "deploy v2.1"
./cache-warmer flush --reason "nginx cache cleared"
```

## 📝 Commands

| Command | Description |
|---------|-------------|
| `init` | Create config.toml |
| `status [--recent N] [--failed N]` | Show dashboard with statistics |
| `once` | Run once and stop |
| `run` | Run continuously (repeats every X seconds) |
| `flush [--reason "text"]` | Mark cache flush (forces rewarm) |

All commands accept the `--config path/to/config.toml` flag.

## ⚙️ Configuration Options

### [app]
- `db_path`: SQLite database location
- `log_file`: Log file location (optional)
- `log_level`: INFO, DEBUG, WARNING, ERROR
- `rewarm_after_hours`: How often to rewarm URLs (default: 24 hours)
- `loop`: true = keep running, false = stop after one run
- `loop_interval_seconds`: Wait time between loops (default: 900 = 15 min)

### [http]
- `user_agent`: Custom User-Agent header
- `timeout_seconds`: HTTP request timeout
- `connect_timeout_seconds`: Connection timeout
- `max_redirects`: Maximum number of redirects to follow
- `concurrency`: Number of concurrent requests (8-32 recommended)
- `min_delay_ms`: Minimum delay between requests (rate limiting)
- `retries`: Number of retry attempts on failures
- `retry_backoff_seconds`: Backoff multiplier for retries

### [load]
- `max_load`: Maximum 1-minute load average (CPU protection)
- `check_interval_seconds`: How often to check load

### [sitemaps]
- `urls`: Array of sitemap URLs

## 🔧 Production Setup

### With Systemd

Create `/etc/systemd/system/cache-warmer.service`:

```ini
[Unit]
Description=Cache Warmer
After=network.target

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/cache-warmer
ExecStart=/opt/cache-warmer/cache-warmer run
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start:
```bash
sudo systemctl enable cache-warmer
sudo systemctl start cache-warmer
sudo systemctl status cache-warmer
```

### With Supervisor

Create `/etc/supervisor/conf.d/cache-warmer.conf`:

```ini
[program:cache-warmer]
command=/opt/cache-warmer/cache-warmer run
directory=/opt/cache-warmer
autostart=true
autorestart=true
stderr_logfile=/var/log/cache-warmer.err.log
stdout_logfile=/var/log/cache-warmer.out.log
user=www-data
```

Reload supervisor:
```bash
sudo supervisorctl reread
sudo supervisorctl update
sudo supervisorctl start cache-warmer
```

## 🐛 Troubleshooting

### Build errors with sqlite3

```bash
# Ensure gcc is installed
sudo apt-get install build-essential  # Ubuntu/Debian
sudo yum install gcc                    # CentOS/RHEL

# Build with CGO enabled
CGO_ENABLED=1 go build -o cache-warmer cache-warmer.go
```

### "Config not found"

```bash
./cache-warmer init
```

### Many failures in dashboard

- Check if URLs are still valid
- Check firewall/IP whitelist
- Increase timeout in config
- Check server logs for rate limiting

### Warmer is slow

- Increase `concurrency` (e.g. to 16 or 32)
- Decrease `min_delay_ms`
- Check `max_load` setting (too low = lots of waiting)

### Load monitoring doesn't work

Load monitoring uses `/proc/loadavg` and only works on Linux. On other systems, load checking is skipped.

## 🎯 Performance Benefits vs Python

| Aspect | Go | Python (aiohttp) |
|--------|----|--------------------|
| Startup time | < 0.1s | ~1-2s |
| Memory usage | ~20-50MB | ~100-200MB |
| Deployment | Single binary | Runtime + dependencies |
| Concurrency | Native goroutines | asyncio event loop |
| CPU efficiency | Compiled | Interpreted |

## 🔄 GitHub Actions CI/CD

The project includes a GitHub Action (`.github/workflows/build.yml`) that automatically:

- Builds Linux binaries on every push to main
- Uploads binaries as artifacts (kept for 90 days)
- Creates a GitHub Release for version tags (v1.0.0, etc.)

### Creating a Release

```bash
git tag v1.0.0
git push origin v1.0.0
```

The Action builds automatically and creates a release with the binaries.

## 📊 Database Schema

SQLite database with 3 tables:

**warmed_url**: URL warming status
```sql
CREATE TABLE warmed_url (
  url TEXT PRIMARY KEY,
  last_warmed_utc TEXT,
  last_status INTEGER,
  last_error TEXT,
  warmed_count INTEGER DEFAULT 0
);
```

**sitemap_seen**: Sitemap fetch status
```sql
CREATE TABLE sitemap_seen (
  sitemap_url TEXT PRIMARY KEY,
  last_fetched_utc TEXT,
  last_error TEXT
);
```

**meta**: Metadata (cache flush tracking)
```sql
CREATE TABLE meta (
  k TEXT PRIMARY KEY,
  v TEXT
);
```

## 🤝 Contributing

Improvements and bug fixes are welcome! Open an issue or pull request.

## 🚀 Creating Releases

If you host this tool on GitHub, you can easily create releases:

```bash
# Create a version tag
git tag -a v1.0.0 -m "Release v1.0.0"

# Push to GitHub
git push origin v1.0.0
```

The GitHub Action builds binaries automatically and creates a release! See [RELEASE.md](RELEASE.md) for details.

### Download Locations

After a release, binaries are available at:

1. **GitHub Releases** (recommended)
   - `https://github.com/hpowernl/cache-warmer/releases`
   - Direct download links
   - Checksums for verification

2. **GitHub Actions Artifacts**
   - Go to Actions → latest build
   - Download artifacts (kept for 90 days)

3. **Install Script**
   - `curl -sSL https://raw.githubusercontent.com/hpowernl/cache-warmer/main/install.sh | bash`
   - Automatically installs the latest release

## 📄 License

MIT License - see [LICENSE](LICENSE) file for details

## 🙋 Support

For questions or issues:
- Open a [GitHub Issue](https://github.com/hpowernl/cache-warmer/issues)
- Check the [Troubleshooting](#-troubleshooting) section
- See [RELEASE.md](RELEASE.md) for release information
