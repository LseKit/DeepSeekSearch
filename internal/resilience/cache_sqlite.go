// 文件名: cache_sqlite.go
// 用途: 实现 SQLite 缓存的建库、CRUD、周期 Prune 与降级复位；淘汰簿记见 cache.go。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，无 CGO（ADR-006）
)

const (
	cacheDegradedThreshold = 3
	cacheCloseTimeout      = 5 * time.Second
)

var errCacheClosed = errors.New("cache is closed")

// 建表 SQL 与 ADR-006 §表结构逐字一致；inflight 额外含 last_error_code 列（见该 ADR 修订说明）。
const (
	cacheTableSQL = `CREATE TABLE IF NOT EXISTS cache (
    capture_id TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    canonical  TEXT NOT NULL,
    body       BLOB NOT NULL,
    nbytes     INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    hits       INTEGER NOT NULL DEFAULT 0,
    last_hit_at INTEGER NOT NULL DEFAULT 0
)`
	cacheCanonicalIndexSQL = `CREATE INDEX IF NOT EXISTS idx_cache_canonical ON cache(canonical, kind)`
	cacheExpiresIndexSQL   = `CREATE INDEX IF NOT EXISTS idx_cache_expires ON cache(expires_at)`
	inflightTableSQL       = `CREATE TABLE IF NOT EXISTS inflight (
    k               TEXT PRIMARY KEY,
    owner           TEXT NOT NULL,
    expires_at      INTEGER NOT NULL,
    last_error_code INTEGER NOT NULL DEFAULT 0
)`
)

type sqliteCache struct {
	db     *sql.DB
	cfg    Config
	clk    Clock
	logger cacheLogger

	hitsMu sync.Mutex
	hits   map[string]*hitStat

	failures atomic.Int32
	degraded atomic.Bool
	closed   atomic.Bool

	stop context.CancelFunc
	ctx  context.Context
	done chan struct{}

	approxEntries atomic.Int64
	approxBytes   atomic.Int64
	needEvict     atomic.Bool
}

// NewSqliteCache 构造 SQLite 缓存。
//
// 目录不存在时创建；打开或建表失败返回 error，由调用方决定降级还是拒绝启动。
// 缓存是「省钱」不是「功能」：运行期的任何 DB 错误只记 degraded，不代表工具失败。
func NewSqliteCache(cfg Config) (Cache, error) { //nolint:gocritic // 签名由 11 号 §四 固定
	withCacheDefaults(&cfg)
	if cfg.Clock == nil {
		cfg.Clock = SystemClock()
	}
	if err := ensureCacheDir(cfg.CacheDbPath); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", cfg.CacheDbPath)
	if err != nil {
		return nil, fmt.Errorf("open cache db: %w", err)
	}
	if cfg.CacheDbPath == ":memory:" {
		// :memory: 是每连接一个独立库；连接池开出第二条连接就看不到表。
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	initCtx, cancel := context.WithTimeout(context.Background(), cacheCloseTimeout)
	defer cancel()
	if initErr := initCacheDB(initCtx, db); initErr != nil {
		return nil, joinClose(initErr, db)
	}

	c := &sqliteCache{
		db:     db,
		cfg:    cfg,
		clk:    cfg.Clock,
		logger: cfg.Logger,
		hits:   make(map[string]*hitStat),
		done:   make(chan struct{}),
	}
	c.ctx, c.stop = context.WithCancel(context.Background())
	entries, nbytes, statErr := queryCacheStats(initCtx, db)
	if statErr != nil {
		return nil, joinClose(statErr, db)
	}
	c.approxEntries.Store(int64(entries))
	c.approxBytes.Store(nbytes)
	c.startPrune()
	return c, nil
}

func joinClose(err error, db *sql.DB) error {
	if closeErr := db.Close(); closeErr != nil {
		return errors.Join(err, fmt.Errorf("close cache db: %w", closeErr))
	}
	return err
}

func withCacheDefaults(cfg *Config) {
	if cfg.CacheDbPath == "" {
		cfg.CacheDbPath = "Data/Cache.db"
	}
	if cfg.CacheMaxEntries <= 0 {
		cfg.CacheMaxEntries = 500
	}
	if cfg.CacheMaxBytesMB <= 0 {
		cfg.CacheMaxBytesMB = 256
	}
	if cfg.CacheMaxEntryKB <= 0 {
		cfg.CacheMaxEntryKB = 2048
	}
	if cfg.CachePruneIntervalS <= 0 {
		cfg.CachePruneIntervalS = 300
	}
	if cfg.TotalTimeoutSearchMs <= 0 {
		cfg.TotalTimeoutSearchMs = 67000
	}
	if cfg.TotalTimeoutFetchMs <= 0 {
		cfg.TotalTimeoutFetchMs = 110000
	}
}

func ensureCacheDir(path string) error {
	if path == ":memory:" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	return nil
}

func initCacheDB(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		return fmt.Errorf("set journal_mode: %w", err)
	}
	for _, pragma := range []string{
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA temp_store = MEMORY",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("apply pragma %q: %w", pragma, err)
		}
	}
	for _, stmt := range []string{
		cacheTableSQL, cacheCanonicalIndexSQL, cacheExpiresIndexSQL, inflightTableSQL,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create cache schema: %w", err)
		}
	}
	return nil
}

func queryCacheStats(ctx context.Context, db *sql.DB) (entries int, nbytes int64, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(nbytes), 0) FROM cache`).Scan(&entries, &nbytes)
	if err != nil {
		return 0, 0, fmt.Errorf("query cache stats: %w", err)
	}
	return entries, nbytes, nil
}

func (c *sqliteCache) Get(ctx context.Context, key string) (body []byte, hit bool, err error) {
	if c.closed.Load() {
		return nil, false, errCacheClosed
	}
	storageKey := cacheKey(key)
	var expiresAt int64
	err = c.db.QueryRowContext(ctx,
		`SELECT body, expires_at FROM cache WHERE capture_id = ?`, storageKey).
		Scan(&body, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		c.recordSuccess()
		return nil, false, nil
	}
	if err != nil {
		c.recordFailure(err)
		return nil, false, fmt.Errorf("cache get: %w", err)
	}
	if expiresAt <= c.clk.Now().Unix() {
		if _, delErr := c.db.ExecContext(ctx,
			`DELETE FROM cache WHERE capture_id = ?`, storageKey); delErr != nil {
			c.recordFailure(delErr)
			return nil, false, fmt.Errorf("cache delete expired: %w", delErr)
		}
		c.recordSuccess()
		return nil, false, nil
	}
	c.recordHit(storageKey)
	c.recordSuccess()
	return body, true, nil
}

func (c *sqliteCache) Put(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if c.closed.Load() {
		return errCacheClosed
	}
	storageKey := cacheKey(key)
	if ttl <= 0 {
		if _, err := c.db.ExecContext(ctx,
			`DELETE FROM cache WHERE capture_id = ?`, storageKey); err != nil {
			c.recordFailure(err)
			return fmt.Errorf("cache put with non-positive ttl: %w", err)
		}
		c.recordSuccess()
		return nil
	}
	limit := int64(c.cfg.CacheMaxEntryKB) * 1024
	if limit > 0 && int64(len(val)) > limit {
		c.logWarn(evtCacheOversize,
			"key_hash", cacheKeyHash(storageKey),
			"bytes", len(val),
			"limit_kb", c.cfg.CacheMaxEntryKB)
		c.recordSuccess()
		return nil
	}
	now := c.clk.Now()
	_, err := c.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO cache
         (capture_id, kind, canonical, body, nbytes, created_at, expires_at, hits, last_hit_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0)`,
		storageKey, cacheKind(storageKey), storageKey, val, len(val),
		now.Unix(), now.Add(ttl).Unix())
	if err != nil {
		c.recordFailure(err)
		return fmt.Errorf("cache put: %w", err)
	}
	c.recordSuccess()
	c.approxEntries.Add(1)
	c.approxBytes.Add(int64(len(val)))
	if c.overCapacity() {
		c.needEvict.Store(true)
	}
	return nil
}

func (c *sqliteCache) Stats() (entries int, bytes int64, degraded bool) {
	if c.closed.Load() {
		return 0, 0, true
	}
	entries, nbytes, err := queryCacheStats(context.Background(), c.db)
	if err != nil {
		c.recordFailure(err)
		return 0, 0, true
	}
	c.recordSuccess()
	return entries, nbytes, false
}

func (c *sqliteCache) Prune(ctx context.Context) (removed int, err error) {
	if c.closed.Load() {
		return 0, errCacheClosed
	}
	res, err := c.db.ExecContext(ctx,
		`DELETE FROM cache WHERE expires_at < ?`, c.clk.Now().Unix())
	if err != nil {
		c.recordFailure(err)
		return 0, fmt.Errorf("cache prune expired: %w", err)
	}
	affected, rowErr := res.RowsAffected()
	if rowErr != nil {
		c.recordFailure(rowErr)
		return 0, fmt.Errorf("cache prune rows affected: %w", rowErr)
	}
	if flushErr := c.flushHits(ctx); flushErr != nil {
		c.recordFailure(flushErr)
		return int(affected), fmt.Errorf("cache prune hits: %w", flushErr)
	}
	evicted := 0
	if c.needEvict.Swap(false) {
		evicted, err = c.evict(ctx)
		if err != nil {
			c.recordFailure(err)
			return int(affected), fmt.Errorf("cache prune evict: %w", err)
		}
	}
	c.recordSuccess()
	c.logInfo(evtCachePrune, "removed", affected, "evicted", evicted)
	return int(affected), nil
}

func (c *sqliteCache) Close() (err error) {
	if c.closed.Swap(true) {
		return nil
	}
	c.stop()
	<-c.done
	ctx, cancel := context.WithTimeout(context.Background(), cacheCloseTimeout)
	defer cancel()
	if flushErr := c.flushHits(ctx); flushErr != nil {
		err = errors.Join(err, flushErr)
	}
	if closeErr := c.db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close cache db: %w", closeErr))
	}
	return err
}

func (c *sqliteCache) overCapacity() bool {
	maxEntries := int64(c.cfg.CacheMaxEntries)
	maxBytes := int64(c.cfg.CacheMaxBytesMB) << 20
	if maxEntries > 0 && c.approxEntries.Load() > maxEntries {
		return true
	}
	return maxBytes > 0 && c.approxBytes.Load() > maxBytes
}

func (c *sqliteCache) startPrune() {
	if c.cfg.CachePruneIntervalS <= 0 {
		close(c.done)
		return
	}
	go c.pruneLoop()
}

func (c *sqliteCache) pruneLoop() {
	defer close(c.done)
	interval := time.Duration(c.cfg.CachePruneIntervalS) * time.Second
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.clk.After(interval):
			if _, pruneErr := c.Prune(c.ctx); pruneErr != nil {
				// Prune 内部已记 warn.cache_degraded；调度器不因单次失败退出。
				continue
			}
		}
	}
}

func (c *sqliteCache) recordFailure(err error) {
	failures := c.failures.Add(1)
	if failures >= cacheDegradedThreshold && c.degraded.CompareAndSwap(false, true) {
		c.logWarn(evtCacheDegraded, "failures", failures, "err", err.Error())
	}
}

func (c *sqliteCache) recordSuccess() {
	c.failures.Store(0)
	if c.degraded.CompareAndSwap(true, false) {
		c.logInfo(evtCacheRecovered)
	}
}

func (c *sqliteCache) logWarn(event string, args ...any) {
	if c.logger != nil {
		c.logger.Warn(event, args...)
	}
}

func (c *sqliteCache) logInfo(event string, args ...any) {
	if c.logger != nil {
		c.logger.Info(event, args...)
	}
}
