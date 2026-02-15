package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fatih/color"
	_ "github.com/mattn/go-sqlite3"
)

// ============================
// Configuration
// ============================

// HTTP status code constants
const (
	httpStatusOK         = 200
	httpStatusClientErr  = 400
	httpStatusSuccessMax = 399
	httpStatusTooMany    = 429
)

// Display truncation limits for status output
const (
	truncateURLLong      = 50
	truncateURLShort     = 45
	truncateURLSitemap   = 55
	truncateErrorMsg     = 30
	maxTimestampDisplay  = 19
)

const defaultConfigTOML = `[app]
# Paths are resolved relative to this config file location.
db_path = "warmer.db"
log_file = "logs/cache_warmer.log"
log_level = "INFO"

# Rewarm URLs if last warm is older than this many hours (unless a flush happened after that warm).
rewarm_after_hours = 24

# If loop=true, keeps running and re-processes sitemaps every loop_interval_seconds
loop = true
loop_interval_seconds = 900

[http]
user_agent = "CacheWarmer/1.0 (+cachewarmer)"
timeout_seconds = 20
connect_timeout_seconds = 10
max_redirects = 5

# Concurrency / pacing
concurrency = 8
min_delay_ms = 50

# Retries
retries = 2
retry_backoff_seconds = 1.0

# 429 rate limit handling
rate_limit_cooldown_seconds = 120
rate_limit_recover_after = 50

[load]
# 1-minute load average limit. For 4 CPUs and "must not exceed 3", use 2.0.
max_load = 2.0
check_interval_seconds = 2

[sitemaps]
urls = [
  "https://www.demoshop.nl/sitemap.xml"
]
`

type Config struct {
	App      AppConfig      `toml:"app"`
	HTTP     HTTPConfig     `toml:"http"`
	Load     LoadConfig     `toml:"load"`
	Sitemaps SitemapsConfig `toml:"sitemaps"`
}

type AppConfig struct {
	DBPath              string `toml:"db_path"`
	LogFile             string `toml:"log_file"`
	LogLevel            string `toml:"log_level"`
	RewarmAfterHours    int    `toml:"rewarm_after_hours"`
	Loop                bool   `toml:"loop"`
	LoopIntervalSeconds int    `toml:"loop_interval_seconds"`
}

type HTTPConfig struct {
	UserAgent                  string  `toml:"user_agent"`
	TimeoutSeconds             int     `toml:"timeout_seconds"`
	ConnectTimeoutSeconds      int     `toml:"connect_timeout_seconds"`
	MaxRedirects               int     `toml:"max_redirects"`
	Concurrency               int     `toml:"concurrency"`
	MinDelayMS                int     `toml:"min_delay_ms"`
	Retries                   int     `toml:"retries"`
	RetryBackoffSeconds       float64 `toml:"retry_backoff_seconds"`
	RateLimitCooldownSeconds  int     `toml:"rate_limit_cooldown_seconds"`
	RateLimitRecoverAfter     int     `toml:"rate_limit_recover_after"`
}

type LoadConfig struct {
	MaxLoad              float64 `toml:"max_load"`
	CheckIntervalSeconds int     `toml:"check_interval_seconds"`
}

type SitemapsConfig struct {
	URLs []string `toml:"urls"`
}

// ============================
// Database
// ============================

const schema = `
CREATE TABLE IF NOT EXISTS warmed_url (
  url TEXT PRIMARY KEY,
  last_warmed_utc TEXT,
  last_status INTEGER,
  last_error TEXT,
  warmed_count INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS sitemap_seen (
  sitemap_url TEXT PRIMARY KEY,
  last_fetched_utc TEXT,
  last_error TEXT
);

CREATE TABLE IF NOT EXISTS meta (
  k TEXT PRIMARY KEY,
  v TEXT
);
`

type WarmDB struct {
	db *sql.DB
}

func NewWarmDB(path string) (*WarmDB, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, err
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	return &WarmDB{db: db}, nil
}

func (w *WarmDB) Close() error {
	return w.db.Close()
}

func (w *WarmDB) GetLastFlush() (*time.Time, error) {
	var v string
	err := w.db.QueryRow("SELECT v FROM meta WHERE k='last_flush_utc'").Scan(&v)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, nil
	}
	return &t, nil
}

func (w *WarmDB) MarkFlush(reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := w.db.Exec(`INSERT INTO meta(k, v) VALUES('last_flush_utc', ?) 
		ON CONFLICT(k) DO UPDATE SET v=excluded.v`, now)
	if err != nil {
		return err
	}

	if reason != "" {
		_, err = w.db.Exec(`INSERT INTO meta(k, v) VALUES('last_flush_reason', ?) 
			ON CONFLICT(k) DO UPDATE SET v=excluded.v`, reason)
	}
	return err
}

func (w *WarmDB) ShouldWarm(url string, rewarmAfter time.Duration) (bool, error) {
	lastFlush, err := w.GetLastFlush()
	if err != nil {
		return false, err
	}

	var lastWarmedStr string
	err = w.db.QueryRow("SELECT last_warmed_utc FROM warmed_url WHERE url = ?", url).Scan(&lastWarmedStr)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	lastWarmed, err := time.Parse(time.RFC3339, lastWarmedStr)
	if err != nil {
		return true, nil
	}

	// If cache flush happened after last warm, rewarm
	if lastFlush != nil && lastWarmed.Before(*lastFlush) {
		return true, nil
	}

	// Otherwise apply normal rewarm policy
	return time.Since(lastWarmed) >= rewarmAfter, nil
}

func (w *WarmDB) MarkWarmed(url string, status int, errorMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	var count int
	err := w.db.QueryRow("SELECT warmed_count FROM warmed_url WHERE url = ?", url).Scan(&count)

	if err == sql.ErrNoRows {
		_, err = w.db.Exec(`INSERT INTO warmed_url(url, last_warmed_utc, last_status, last_error, warmed_count) 
			VALUES(?,?,?,?,1)`, url, now, status, errorMsg)
		return err
	}

	if err != nil {
		return err
	}

	_, err = w.db.Exec(`UPDATE warmed_url SET last_warmed_utc=?, last_status=?, last_error=?, warmed_count=warmed_count+1 
		WHERE url=?`, now, status, errorMsg, url)
	return err
}

func (w *WarmDB) MarkSitemap(sitemapURL string, errorMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	var exists bool
	err := w.db.QueryRow("SELECT 1 FROM sitemap_seen WHERE sitemap_url = ?", sitemapURL).Scan(&exists)

	if err == sql.ErrNoRows {
		_, err = w.db.Exec(`INSERT INTO sitemap_seen(sitemap_url, last_fetched_utc, last_error) 
			VALUES(?,?,?)`, sitemapURL, now, errorMsg)
		return err
	}

	if err != nil {
		return err
	}

	_, err = w.db.Exec(`UPDATE sitemap_seen SET last_fetched_utc=?, last_error=? 
		WHERE sitemap_url=?`, now, errorMsg, sitemapURL)
	return err
}

type Stats struct {
	WarmedTotal  int
	OKTotal      int
	ErrTotal     int
	LastFlushUTC string
}

func (w *WarmDB) Stats() (*Stats, error) {
	var s Stats

	err := w.db.QueryRow("SELECT COUNT(*) FROM warmed_url").Scan(&s.WarmedTotal)
	if err != nil {
		return nil, err
	}

	err = w.db.QueryRow(`SELECT COUNT(*) FROM warmed_url 
		WHERE last_error IS NULL AND last_status BETWEEN ? AND ?`, httpStatusOK, httpStatusSuccessMax).Scan(&s.OKTotal)
	if err != nil {
		return nil, err
	}

	err = w.db.QueryRow(`SELECT COUNT(*) FROM warmed_url 
		WHERE last_error IS NOT NULL OR last_status >= ? OR last_status = 0`, httpStatusClientErr).Scan(&s.ErrTotal)
	if err != nil {
		return nil, err
	}

	lastFlush, err := w.GetLastFlush()
	if err != nil {
		return nil, fmt.Errorf("getting last flush: %w", err)
	}
	if lastFlush != nil {
		s.LastFlushUTC = lastFlush.Format(time.RFC3339)
	}

	return &s, nil
}

type RecentURL struct {
	URL       string
	Timestamp string
	Status    int
	Error     sql.NullString
}

func (w *WarmDB) GetRecentWarmed(limit int) ([]RecentURL, error) {
	rows, err := w.db.Query(`SELECT url, last_warmed_utc, last_status, last_error 
		FROM warmed_url ORDER BY last_warmed_utc DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RecentURL
	for rows.Next() {
		var r RecentURL
		if err := rows.Scan(&r.URL, &r.Timestamp, &r.Status, &r.Error); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (w *WarmDB) GetFailedURLs(limit int) ([]RecentURL, error) {
	rows, err := w.db.Query(`SELECT url, last_warmed_utc, last_status, last_error 
		FROM warmed_url 
		WHERE last_error IS NOT NULL OR last_status >= ? OR last_status = 0 
		ORDER BY last_warmed_utc DESC LIMIT ?`, httpStatusClientErr, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RecentURL
	for rows.Next() {
		var r RecentURL
		if err := rows.Scan(&r.URL, &r.Timestamp, &r.Status, &r.Error); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

type SitemapStatus struct {
	URL       string
	Timestamp string
	Error     sql.NullString
}

func (w *WarmDB) GetSitemapStatus() ([]SitemapStatus, error) {
	rows, err := w.db.Query(`SELECT sitemap_url, last_fetched_utc, last_error 
		FROM sitemap_seen ORDER BY last_fetched_utc DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SitemapStatus
	for rows.Next() {
		var s SitemapStatus
		if err := rows.Scan(&s.URL, &s.Timestamp, &s.Error); err != nil {
			return nil, err
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// ============================
// Sitemap Parsing
// ============================

type Sitemap struct {
	XMLName xml.Name       `xml:"urlset"`
	URLs    []SitemapURL   `xml:"url"`
	Sitemap []SitemapIndex `xml:"sitemap"`
}

type SitemapURL struct {
	Loc string `xml:"loc"`
}

type SitemapIndex struct {
	Loc string `xml:"loc"`
}

type SitemapIndexRoot struct {
	XMLName  xml.Name       `xml:"sitemapindex"`
	Sitemaps []SitemapIndex `xml:"sitemap"`
}

func parseSitemapXML(data []byte) ([]string, []string, error) {
	var childSitemaps []string
	var urls []string

	// Try parsing as sitemapindex
	var idx SitemapIndexRoot
	if err := xml.Unmarshal(data, &idx); err == nil && len(idx.Sitemaps) > 0 {
		for _, s := range idx.Sitemaps {
			if s.Loc != "" {
				childSitemaps = append(childSitemaps, strings.TrimSpace(s.Loc))
			}
		}
	}

	// Try parsing as urlset
	var urlset Sitemap
	if err := xml.Unmarshal(data, &urlset); err == nil {
		for _, u := range urlset.URLs {
			if u.Loc != "" {
				urls = append(urls, strings.TrimSpace(u.Loc))
			}
		}
		for _, s := range urlset.Sitemap {
			if s.Loc != "" {
				childSitemaps = append(childSitemaps, strings.TrimSpace(s.Loc))
			}
		}
	}

	return childSitemaps, urls, nil
}

// ============================
// Load Monitoring
// ============================

func getLoad1m() (float64, error) {
	// Try to read /proc/loadavg on Linux
	data, err := os.ReadFile("/proc/loadavg")
	if err == nil {
		var load1, load5, load15 float64
		_, err := fmt.Sscanf(string(data), "%f %f %f", &load1, &load5, &load15)
		if err == nil {
			return load1, nil
		}
	}

	// Fallback: load monitoring not available on this platform
	return 0, fmt.Errorf("load monitoring not available on this platform")
}

func waitForLoad(ctx context.Context, cfg LoadConfig) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		load, err := getLoad1m()
		if err != nil {
			// Cannot measure load, don't block
			return nil
		}

		if load <= cfg.MaxLoad {
			return nil
		}

		log.Printf("Load too high (1m=%.2f > max=%.2f). Sleeping %ds...",
			load, cfg.MaxLoad, cfg.CheckIntervalSeconds)

		select {
		case <-time.After(time.Duration(cfg.CheckIntervalSeconds) * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ============================
// Rate Limiter (429 adaptive)
// ============================

type rateLimiter struct {
	mu                sync.Mutex
	cond              *sync.Cond
	currentConcurrency int
	minConcurrency     int
	maxConcurrency     int
	activeWorkers     int
	cooldownUntil     time.Time
	consecutiveOK     int
	recoverAfter      int
	cooldownSeconds   int
}

func newRateLimiter(concurrency, cooldownSeconds, recoverAfter int) *rateLimiter {
	rl := &rateLimiter{
		currentConcurrency: concurrency,
		minConcurrency:     1,
		maxConcurrency:     concurrency,
		activeWorkers:      0,
		cooldownUntil:      time.Time{},
		consecutiveOK:      0,
		recoverAfter:       recoverAfter,
		cooldownSeconds:    cooldownSeconds,
	}
	rl.cond = sync.NewCond(&rl.mu)
	return rl
}

func (rl *rateLimiter) acquire(ctx context.Context) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		now := time.Now()
		if now.Before(rl.cooldownUntil) {
			d := time.Until(rl.cooldownUntil)
			rl.mu.Unlock()
			select {
			case <-ctx.Done():
				rl.mu.Lock()
				return ctx.Err()
			case <-time.After(d):
			}
			rl.mu.Lock()
			continue
		}
		if rl.activeWorkers < rl.currentConcurrency {
			rl.activeWorkers++
			return nil
		}
		rl.cond.Wait()
	}
}

func (rl *rateLimiter) release() {
	rl.mu.Lock()
	rl.activeWorkers--
	rl.cond.Broadcast()
	rl.mu.Unlock()
}

func (rl *rateLimiter) on429(retryAfter time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	newConcurrency := rl.currentConcurrency / 2
	if newConcurrency < rl.minConcurrency {
		newConcurrency = rl.minConcurrency
	}
	oldConcurrency := rl.currentConcurrency
	rl.currentConcurrency = newConcurrency
	rl.consecutiveOK = 0
	cooldown := retryAfter
	if cooldown < time.Duration(rl.cooldownSeconds)*time.Second {
		cooldown = time.Duration(rl.cooldownSeconds) * time.Second
	}
	rl.cooldownUntil = time.Now().Add(cooldown)
	rl.cond.Broadcast()
	log.Printf("429 rate limit: concurrency reduced %d -> %d, cooldown %.0fs", oldConcurrency, newConcurrency, cooldown.Seconds())
	if newConcurrency == rl.minConcurrency {
		log.Printf("429 rate limit: concurrency at minimum (%d worker); crawling at slowest pace", rl.minConcurrency)
	}
}

func (rl *rateLimiter) onSuccess() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.consecutiveOK++
	if rl.consecutiveOK >= rl.recoverAfter && rl.currentConcurrency < rl.maxConcurrency {
		oldConcurrency := rl.currentConcurrency
		rl.currentConcurrency++
		rl.consecutiveOK = 0
		log.Printf("429 rate limit: concurrency recovered %d -> %d", oldConcurrency, rl.currentConcurrency)
	}
}

// parseRetryAfter parses the Retry-After header. Returns 0 if unparseable.
func parseRetryAfter(hdr string, defaultSec int) time.Duration {
	hdr = strings.TrimSpace(hdr)
	if hdr == "" {
		return time.Duration(defaultSec) * time.Second
	}
	if sec, err := strconv.Atoi(hdr); err == nil && sec >= 0 {
		return time.Duration(sec) * time.Second
	}
	if t, err := time.Parse(time.RFC1123, hdr); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return time.Duration(defaultSec) * time.Second
}

// ============================
// Cache Warmer
// ============================

type CacheWarmer struct {
	cfg          Config
	db           *WarmDB
	client       *http.Client
	rl           *rateLimiter
	seenSitemaps map[string]bool
	mu           sync.Mutex
}

func NewCacheWarmer(cfg Config, db *WarmDB) *CacheWarmer {
	client := &http.Client{
		Timeout: time.Duration(cfg.HTTP.TimeoutSeconds) * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= cfg.HTTP.MaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	cooldownSec := cfg.HTTP.RateLimitCooldownSeconds
	if cooldownSec <= 0 {
		cooldownSec = 120
	}
	recoverAfter := cfg.HTTP.RateLimitRecoverAfter
	if recoverAfter <= 0 {
		recoverAfter = 50
	}
	rl := newRateLimiter(cfg.HTTP.Concurrency, cooldownSec, recoverAfter)

	return &CacheWarmer{
		cfg:          cfg,
		db:           db,
		client:       client,
		rl:           rl,
		seenSitemaps: make(map[string]bool),
	}
}

func (c *CacheWarmer) fetchBytes(ctx context.Context, url string) ([]byte, error) {
	var lastErr error

	for attempt := 1; attempt <= c.cfg.HTTP.Retries+1; attempt++ {
		if err := waitForLoad(ctx, c.cfg.Load); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.cfg.HTTP.UserAgent)

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt >= c.cfg.HTTP.Retries+1 {
				break
			}
			backoff := time.Duration(float64(attempt)*c.cfg.HTTP.RetryBackoffSeconds) * time.Second
			log.Printf("Fetch failed (%v) attempt %d/%d for %s; sleeping %.1fs",
				err, attempt, c.cfg.HTTP.Retries+1, url, backoff.Seconds())
			time.Sleep(backoff)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			lastErr = err
			if attempt >= c.cfg.HTTP.Retries+1 {
				break
			}
			backoff := time.Duration(float64(attempt)*c.cfg.HTTP.RetryBackoffSeconds) * time.Second
			time.Sleep(backoff)
			continue
		}

		if resp.StatusCode >= httpStatusClientErr {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			if attempt >= c.cfg.HTTP.Retries+1 {
				break
			}
			backoff := time.Duration(float64(attempt)*c.cfg.HTTP.RetryBackoffSeconds) * time.Second
			time.Sleep(backoff)
			continue
		}

		// Decompress if .gz
		if strings.HasSuffix(strings.ToLower(url), ".gz") {
			reader, err := gzip.NewReader(strings.NewReader(string(body)))
			if err != nil {
				lastErr = fmt.Errorf("gzip.NewReader: %w", err)
				if attempt >= c.cfg.HTTP.Retries+1 {
					break
				}
				backoff := time.Duration(float64(attempt)*c.cfg.HTTP.RetryBackoffSeconds) * time.Second
				log.Printf("Gzip decompress failed for %s: %v; retrying in %.1fs", url, err, backoff.Seconds())
				time.Sleep(backoff)
				continue
			}
			decompressed, err := io.ReadAll(reader)
			_ = reader.Close()
			if err != nil {
				lastErr = fmt.Errorf("gzip read: %w", err)
				if attempt >= c.cfg.HTTP.Retries+1 {
					break
				}
				backoff := time.Duration(float64(attempt)*c.cfg.HTTP.RetryBackoffSeconds) * time.Second
				log.Printf("Gzip decompress read failed for %s: %v; retrying in %.1fs", url, err, backoff.Seconds())
				time.Sleep(backoff)
				continue
			}
			body = decompressed
		}

		return body, nil
	}

	return nil, lastErr
}

func (c *CacheWarmer) collectURLsFromSitemap(ctx context.Context, sitemapURL string) ([]string, error) {
	c.mu.Lock()
	if c.seenSitemaps[sitemapURL] {
		c.mu.Unlock()
		return nil, nil
	}
	c.seenSitemaps[sitemapURL] = true
	c.mu.Unlock()

	log.Printf("Fetching sitemap: %s", sitemapURL)

	data, err := c.fetchBytes(ctx, sitemapURL)
	if err != nil {
		c.db.MarkSitemap(sitemapURL, err.Error())
		return nil, err
	}

	childSitemaps, urls, err := parseSitemapXML(data)
	if err != nil {
		c.db.MarkSitemap(sitemapURL, err.Error())
		return nil, err
	}

	c.db.MarkSitemap(sitemapURL, "")

	collected := urls

	for _, child := range childSitemaps {
		select {
		case <-ctx.Done():
			return collected, ctx.Err()
		default:
		}

		childURLs, err := c.collectURLsFromSitemap(ctx, child)
		if err != nil {
			log.Printf("Failed to fetch child sitemap %s: %v", child, err)
			continue
		}
		collected = append(collected, childURLs...)
	}

	return collected, nil
}

// warmOne warms a single URL. Returns (status, errMsg, slotReleased).
// If slotReleased is true, the caller must NOT call rl.release() — warmOne already did.
func (c *CacheWarmer) warmOne(ctx context.Context, url string) (status int, errMsg string, slotReleased bool) {
	if c.cfg.HTTP.MinDelayMS > 0 {
		time.Sleep(time.Duration(c.cfg.HTTP.MinDelayMS) * time.Millisecond)
	}

	if err := waitForLoad(ctx, c.cfg.Load); err != nil {
		return 0, err.Error(), false
	}

	cooldownSec := c.cfg.HTTP.RateLimitCooldownSeconds
	if cooldownSec <= 0 {
		cooldownSec = 120
	}

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err().Error(), false
		default:
		}

		var lastErr error
		got429 := false
		var retryAfter429 time.Duration

		for attempt := 1; attempt <= c.cfg.HTTP.Retries+1; attempt++ {
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				return 0, err.Error(), false
			}
			req.Header.Set("User-Agent", c.cfg.HTTP.UserAgent)

			resp, err := c.client.Do(req)
			if err != nil {
				lastErr = err
				if attempt >= c.cfg.HTTP.Retries+1 {
					break
				}
				backoff := time.Duration(float64(attempt)*c.cfg.HTTP.RetryBackoffSeconds) * time.Second
				log.Printf("Warm failed (%v) attempt %d/%d for %s; sleeping %.1fs",
					err, attempt, c.cfg.HTTP.Retries+1, url, backoff.Seconds())
				time.Sleep(backoff)
				continue
			}

			// Read full body to warm cache
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if err != nil {
				lastErr = err
				if attempt >= c.cfg.HTTP.Retries+1 {
					break
				}
				backoff := time.Duration(float64(attempt)*c.cfg.HTTP.RetryBackoffSeconds) * time.Second
				time.Sleep(backoff)
				continue
			}

			if resp.StatusCode == httpStatusTooMany {
				retryAfter429 = parseRetryAfter(resp.Header.Get("Retry-After"), cooldownSec)
				c.rl.on429(retryAfter429)
				log.Printf("429 Too Many Requests for %s -- reducing concurrency, cooling down %.0fs; will retry",
					url, retryAfter429.Seconds())
				got429 = true
				break
			}

			if resp.StatusCode >= httpStatusClientErr {
				lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
				if attempt >= c.cfg.HTTP.Retries+1 {
					return resp.StatusCode, lastErr.Error(), false
				}
				backoff := time.Duration(float64(attempt)*c.cfg.HTTP.RetryBackoffSeconds) * time.Second
				time.Sleep(backoff)
				continue
			}

			c.rl.onSuccess()
			return resp.StatusCode, "", false
		}

		if got429 {
			// Release slot before cooldown to restore invariant activeWorkers <= currentConcurrency.
			// Otherwise we could have activeWorkers=8 and currentConcurrency=4, starving new workers.
			c.rl.release()
			select {
			case <-ctx.Done():
				// Caller must not release again — we already did.
				return 0, ctx.Err().Error(), true
			case <-time.After(retryAfter429):
			}
			if err := c.rl.acquire(ctx); err != nil {
				// Caller must not release again — we already did before cooldown.
				return 0, err.Error(), true
			}
			continue
		}
		if lastErr != nil {
			return 0, lastErr.Error(), false
		}
		return 0, "unreachable", false
	}
}

func (c *CacheWarmer) runOnce(ctx context.Context) (int, int, error) {
	c.seenSitemaps = make(map[string]bool)

	// Collect URLs
	var allURLs []string
	for _, sm := range c.cfg.Sitemaps.URLs {
		select {
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		default:
		}

		urls, err := c.collectURLsFromSitemap(ctx, sm)
		if err != nil {
			log.Printf("Error collecting from sitemap %s: %v", sm, err)
		}
		allURLs = append(allURLs, urls...)
	}

	// De-duplicate
	seen := make(map[string]bool)
	var uniqueURLs []string
	for _, u := range allURLs {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		uniqueURLs = append(uniqueURLs, u)
	}

	log.Printf("Collected %d unique URLs from sitemaps.", len(uniqueURLs))

	// Filter URLs that need warming
	rewarmAfter := time.Duration(c.cfg.App.RewarmAfterHours) * time.Hour
	var toWarm []string
	for _, u := range uniqueURLs {
		shouldWarm, err := c.db.ShouldWarm(u, rewarmAfter)
		if err != nil {
			log.Printf("Error checking if should warm %s: %v", u, err)
			continue
		}
		if shouldWarm {
			toWarm = append(toWarm, u)
		}
	}

	log.Printf("Need to warm %d URLs (rewarm_after=%dh).", len(toWarm), c.cfg.App.RewarmAfterHours)

	// Warm concurrently (atomic counters to avoid race conditions)
	var ok, fail atomic.Int64
	var wg sync.WaitGroup

	for _, url := range toWarm {
		select {
		case <-ctx.Done():
			wg.Wait()
			return int(ok.Load()), int(fail.Load()), ctx.Err()
		default:
		}

		wg.Add(1)
		go func(u string) {
			defer wg.Done()

			if err := c.rl.acquire(ctx); err != nil {
				log.Printf("WARM SKIP %s (context cancelled)", u)
				return
			}
			var slotReleased bool
			defer func() {
				if !slotReleased {
					c.rl.release()
				}
			}()

			status, errMsg, slotReleased := c.warmOne(ctx, u)
			c.db.MarkWarmed(u, status, errMsg)

			if errMsg != "" {
				fail.Add(1)
				log.Printf("WARM FAIL %s error=%s", u, errMsg)
			} else {
				ok.Add(1)
				log.Printf("WARM OK   %s status=%d", u, status)
			}
		}(url)
	}

	wg.Wait()

	okVal, failVal := ok.Load(), fail.Load()
	log.Printf("Run complete. ok=%d fail=%d", okVal, failVal)
	return int(okVal), int(failVal), nil
}

func (c *CacheWarmer) runLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, _, err := c.runOnce(ctx)
		if err != nil && err != context.Canceled {
			log.Printf("Error during run: %v", err)
		}

		if !c.cfg.App.Loop {
			return nil
		}

		log.Printf("Sleeping for %d seconds before next run...", c.cfg.App.LoopIntervalSeconds)

		select {
		case <-time.After(time.Duration(c.cfg.App.LoopIntervalSeconds) * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ============================
// CLI Commands
// ============================

func cmdInit(configPath string, force bool) error {
	if _, err := os.Stat(configPath); err == nil && !force {
		fmt.Printf("Config already exists: %s\n", configPath)
		return nil
	}

	// Calculate max_load based on CPU count (CPU - 1, minimum 1.0)
	numCPU := runtime.NumCPU()
	maxLoad := float64(numCPU - 1)
	if maxLoad < 1.0 {
		maxLoad = 1.0
	}

	// Replace max_load in template with calculated value
	config := strings.Replace(defaultConfigTOML, "max_load = 2.0", fmt.Sprintf("max_load = %.1f", maxLoad), 1)

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return err
	}

	fmt.Printf("Wrote config template: %s\n", configPath)
	fmt.Printf("Detected %d CPU(s), set max_load = %.1f\n", numCPU, maxLoad)
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func truncateTimestamp(s string) string {
	if len(s) >= maxTimestampDisplay {
		return s[:maxTimestampDisplay]
	}
	return s
}

func statusPrintStatistics(stats *Stats, yellow, _ func(a ...interface{}) string) {
	fmt.Println("\n📊", yellow("STATISTICS"))
	fmt.Println(strings.Repeat("-", 70))
	fmt.Printf("  Total URLs Warmed:    %d\n", stats.WarmedTotal)
	fmt.Printf("  Successful (2xx-3xx): %d\n", stats.OKTotal)
	fmt.Printf("  Failed (4xx-5xx):     %d\n", stats.ErrTotal)
	if stats.LastFlushUTC != "" {
		fmt.Printf("  Last Cache Flush:     %s\n", stats.LastFlushUTC)
	} else {
		fmt.Printf("  Last Cache Flush:     Never\n")
	}
}

func statusPrintRecentURLs(db *WarmDB, limit int, green, red, yellow func(a ...interface{}) string) error {
	fmt.Printf("\n✅ %s (%d most recent)\n", yellow("RECENTLY WARMED"), limit)
	fmt.Println(strings.Repeat("-", 70))
	recent, err := db.GetRecentWarmed(limit)
	if err != nil {
		return err
	}
	if len(recent) > 0 {
		for _, r := range recent {
			icon := green("✅")
			if r.Status >= httpStatusClientErr {
				icon = red("❌")
			}
			displayURL := truncate(r.URL, truncateURLLong)
			ts := truncateTimestamp(r.Timestamp)
			fmt.Printf("  %s [%d] %s | %s\n", icon, r.Status, ts, displayURL)
		}
	} else {
		fmt.Println("  (No URLs warmed yet)")
	}
	return nil
}

func statusPrintFailures(db *WarmDB, limit int, red, yellow func(a ...interface{}) string) error {
	fmt.Printf("\n❌ %s (%d most recent)\n", yellow("RECENT FAILURES"), limit)
	fmt.Println(strings.Repeat("-", 70))
	failed, err := db.GetFailedURLs(limit)
	if err != nil {
		return err
	}
	if len(failed) > 0 {
		for _, f := range failed {
			displayURL := truncate(f.URL, truncateURLShort)
			ts := truncateTimestamp(f.Timestamp)
			errorMsg := "(no error msg)"
			if f.Error.Valid {
				errorMsg = truncate(f.Error.String, truncateErrorMsg)
			}
			fmt.Printf("  %s [%d] %s\n", red("❌"), f.Status, ts)
			fmt.Printf("     URL: %s\n", displayURL)
			fmt.Printf("     Error: %s\n", errorMsg)
		}
	} else {
		fmt.Println("  (No failures)")
	}
	return nil
}

func statusPrintSitemaps(db *WarmDB, green, red, yellow func(a ...interface{}) string) error {
	fmt.Printf("\n🗺️  %s\n", yellow("SITEMAP STATUS"))
	fmt.Println(strings.Repeat("-", 70))
	sitemaps, err := db.GetSitemapStatus()
	if err != nil {
		return err
	}
	if len(sitemaps) > 0 {
		for _, sm := range sitemaps {
			icon := green("✅")
			if sm.Error.Valid && sm.Error.String != "" {
				icon = red("❌")
			}
			displayURL := truncate(sm.URL, truncateURLSitemap)
			ts := truncateTimestamp(sm.Timestamp)
			fmt.Printf("  %s %s | %s\n", icon, ts, displayURL)
			if sm.Error.Valid && sm.Error.String != "" {
				fmt.Printf("     Error: %s\n", sm.Error.String)
			}
		}
	} else {
		fmt.Println("  (No sitemaps fetched yet)")
	}
	return nil
}

func cmdStatus(configPath string, showRecent, showFailed int) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	db, err := NewWarmDB(cfg.App.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	stats, err := db.Stats()
	if err != nil {
		return err
	}

	cyan := color.New(color.FgCyan).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()

	fmt.Println()
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("  ", cyan("CACHE WARMER DASHBOARD"))
	fmt.Println(strings.Repeat("=", 70))

	statusPrintStatistics(stats, yellow, green)
	if err := statusPrintRecentURLs(db, showRecent, green, red, yellow); err != nil {
		return err
	}
	if err := statusPrintFailures(db, showFailed, red, yellow); err != nil {
		return err
	}
	if err := statusPrintSitemaps(db, green, red, yellow); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  Config: %s\n", configPath)
	fmt.Printf("  Database: %s\n", cfg.App.DBPath)
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println()

	return nil
}

func cmdFlush(configPath string, reason string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	db, err := NewWarmDB(cfg.App.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if reason == "" {
		reason = "manual flush"
	}

	if err := db.MarkFlush(reason); err != nil {
		return err
	}

	green := color.New(color.FgGreen).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()

	fmt.Println()
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("  ", green("✅ CACHE FLUSH MARKED"))
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("\n  Reason: %s\n", reason)
	fmt.Printf("  Time:   %s\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))

	stats, err := db.Stats()
	if err != nil {
		return err
	}

	fmt.Printf("\n  📊 Current Stats:\n")
	fmt.Printf("     Total URLs warmed: %s\n", cyan(fmt.Sprint(stats.WarmedTotal)))
	fmt.Printf("     %s\n", green("Will be re-warmed on next run!"))
	fmt.Println()
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println()

	log.Printf("Marked cache flush. reason=%s", reason)

	return nil
}

func cmdRun(configPath string, once bool) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	// Setup logging
	if cfg.App.LogFile != "" {
		logDir := filepath.Dir(cfg.App.LogFile)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return err
		}

		f, err := os.OpenFile(cfg.App.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		defer f.Close()

		log.SetOutput(io.MultiWriter(os.Stdout, f))
	}

	db, err := NewWarmDB(cfg.App.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	warmer := NewCacheWarmer(cfg, db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received stop signal, shutting down...")
		cancel()
	}()

	if once {
		log.Printf("Starting cache warmer ONCE. db=%s concurrency=%d max_load=%.2f",
			cfg.App.DBPath, cfg.HTTP.Concurrency, cfg.Load.MaxLoad)
		ok, fail, err := warmer.runOnce(ctx)
		if err != nil && err != context.Canceled {
			return err
		}

		stats, _ := db.Stats()
		log.Printf("Summary: ok=%d fail=%d warmed_total=%d last_flush_utc=%s",
			ok, fail, stats.WarmedTotal, stats.LastFlushUTC)
	} else {
		log.Printf("Starting cache warmer LOOP=%t interval=%ds db=%s concurrency=%d max_load=%.2f",
			cfg.App.Loop, cfg.App.LoopIntervalSeconds, cfg.App.DBPath,
			cfg.HTTP.Concurrency, cfg.Load.MaxLoad)
		if err := warmer.runLoop(ctx); err != nil && err != context.Canceled {
			return err
		}
	}

	log.Println("Stopped.")
	return nil
}

// ============================
// Config Loading
// ============================

// validateConfig checks config values and returns descriptive errors.
func validateConfig(cfg *Config) error {
	// HTTP validation
	if cfg.HTTP.Concurrency < 1 {
		return fmt.Errorf("http.concurrency must be >= 1, got %d", cfg.HTTP.Concurrency)
	}
	if cfg.HTTP.TimeoutSeconds < 1 {
		return fmt.Errorf("http.timeout_seconds must be > 0, got %d", cfg.HTTP.TimeoutSeconds)
	}
	if cfg.HTTP.ConnectTimeoutSeconds < 1 {
		return fmt.Errorf("http.connect_timeout_seconds must be > 0, got %d", cfg.HTTP.ConnectTimeoutSeconds)
	}
	if cfg.HTTP.MaxRedirects < 0 {
		return fmt.Errorf("http.max_redirects must be >= 0, got %d", cfg.HTTP.MaxRedirects)
	}
	if cfg.HTTP.MinDelayMS < 0 {
		return fmt.Errorf("http.min_delay_ms must be >= 0, got %d", cfg.HTTP.MinDelayMS)
	}
	if cfg.HTTP.Retries < 0 {
		return fmt.Errorf("http.retries must be >= 0, got %d", cfg.HTTP.Retries)
	}
	if cfg.HTTP.RetryBackoffSeconds < 0 {
		return fmt.Errorf("http.retry_backoff_seconds must be >= 0, got %f", cfg.HTTP.RetryBackoffSeconds)
	}

	// App validation
	if cfg.App.RewarmAfterHours < 1 {
		return fmt.Errorf("app.rewarm_after_hours must be >= 1, got %d", cfg.App.RewarmAfterHours)
	}
	if cfg.App.Loop && cfg.App.LoopIntervalSeconds < 1 {
		return fmt.Errorf("app.loop_interval_seconds must be >= 1 when loop=true, got %d", cfg.App.LoopIntervalSeconds)
	}

	// Load validation
	if cfg.Load.MaxLoad < 0 {
		return fmt.Errorf("load.max_load must be >= 0, got %f", cfg.Load.MaxLoad)
	}
	if cfg.Load.CheckIntervalSeconds < 1 {
		return fmt.Errorf("load.check_interval_seconds must be >= 1, got %d", cfg.Load.CheckIntervalSeconds)
	}

	// Sitemap URL validation
	for i, u := range cfg.Sitemaps.URLs {
		parsed, err := url.Parse(u)
		if err != nil {
			return fmt.Errorf("sitemaps.urls[%d] invalid URL %q: %w", i, u, err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("sitemaps.urls[%d] must have scheme and host: %q", i, u)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("sitemaps.urls[%d] scheme must be http or https: %q", i, u)
		}
	}

	return nil
}

func loadConfig(configPath string) (Config, error) {
	var cfg Config

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return cfg, fmt.Errorf("config not found: %s (tip: run `cache-warmer init`)", configPath)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg, err
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	if len(cfg.Sitemaps.URLs) == 0 {
		return cfg, fmt.Errorf("no sitemaps configured. Add [sitemaps].urls in config.toml")
	}

	if err := validateConfig(&cfg); err != nil {
		return cfg, fmt.Errorf("config validation: %w", err)
	}

	// Resolve paths relative to config file
	configDir := filepath.Dir(configPath)
	if !filepath.IsAbs(cfg.App.DBPath) {
		cfg.App.DBPath = filepath.Join(configDir, cfg.App.DBPath)
	}
	if cfg.App.LogFile != "" && !filepath.IsAbs(cfg.App.LogFile) {
		cfg.App.LogFile = filepath.Join(configDir, cfg.App.LogFile)
	}

	return cfg, nil
}

// ============================
// Main
// ============================

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: cache-warmer <command> [options]")
		fmt.Println("\nCommands:")
		fmt.Println("  init              Create default config.toml")
		fmt.Println("  status            Show dashboard with current status")
		fmt.Println("  run               Run warmer continuously")
		fmt.Println("  once              Run a single pass and exit")
		fmt.Println("  flush             Mark cache flush (forces rewarm)")
		os.Exit(1)
	}

	command := os.Args[1]

	// Global flags
	configPath := flag.String("config", "config.toml", "Path to config TOML")

	switch command {
	case "init":
		fs := flag.NewFlagSet("init", flag.ExitOnError)
		force := fs.Bool("force", false, "Overwrite existing config")
		fs.Parse(os.Args[2:])

		if err := cmdInit(*configPath, *force); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "status":
		fs := flag.NewFlagSet("status", flag.ExitOnError)
		recent := fs.Int("recent", 10, "Number of recent URLs to show")
		failed := fs.Int("failed", 10, "Number of failed URLs to show")
		configPath := fs.String("config", "config.toml", "Path to config TOML")
		fs.Parse(os.Args[2:])

		if err := cmdStatus(*configPath, *recent, *failed); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "flush":
		fs := flag.NewFlagSet("flush", flag.ExitOnError)
		reason := fs.String("reason", "", "Optional reason for flush")
		configPath := fs.String("config", "config.toml", "Path to config TOML")
		fs.Parse(os.Args[2:])

		if err := cmdFlush(*configPath, *reason); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		configPath := fs.String("config", "config.toml", "Path to config TOML")
		fs.Parse(os.Args[2:])

		if err := cmdRun(*configPath, false); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "once":
		fs := flag.NewFlagSet("once", flag.ExitOnError)
		configPath := fs.String("config", "config.toml", "Path to config TOML")
		fs.Parse(os.Args[2:])

		if err := cmdRun(*configPath, true); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		os.Exit(1)
	}
}
