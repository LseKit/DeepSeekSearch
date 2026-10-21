// 文件名: cache.go
// 用途: 实现缓存 key 归一化、singleflight 共享的 oversize 编解码与淘汰簿记逻辑。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package cache

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	cacheSchemaVersion = "v1"

	evtCacheOversize     = "warn.cache_oversize"
	evtCacheDegraded     = "warn.cache_degraded"
	evtCacheRecovered    = "info.cache_recovered"
	evtCacheCommitFailed = "warn.cache_commit_failed"
	evtCachePrune        = "cache.prune"
)

// cacheKey 给 key 加 schema 版本前缀，避免升级后旧语义的条目被当成新格式返回。
// 已带前缀的 key 原样返回，调用方不必知道前缀由哪一层负责。
func cacheKey(key string) string {
	prefix := cacheSchemaVersion + ":"
	if strings.HasPrefix(key, prefix) {
		return key
	}
	return prefix + key
}

// cacheKind 从归一化后的 key（<tool>:<hash>）取 tool 段，供 kind 列使用。
func cacheKind(storageKey string) string {
	rest := strings.TrimPrefix(storageKey, cacheSchemaVersion+":")
	if i := strings.IndexByte(rest, ':'); i > 0 {
		return rest[:i]
	}
	return "unknown"
}

// cacheKeyHash 返回 key 的短哈希，供日志使用；日志里 MUST NOT 出现完整正文 key。
func cacheKeyHash(storageKey string) string {
	sum := sha256.Sum256([]byte(storageKey))
	return hex.EncodeToString(sum[:6])
}

type hitStat struct {
	total   int64
	flushed int64
	last    time.Time
}

func (c *sqliteCache) recordHit(storageKey string) {
	now := c.clk.Now()
	c.hitsMu.Lock()
	stat := c.hits[storageKey]
	if stat == nil {
		stat = &hitStat{}
		c.hits[storageKey] = stat
	}
	stat.total++
	stat.last = now
	c.hitsMu.Unlock()
}

// flushHits 在 Prune/Close 时批量落盘命中统计。
// Get 路径只累加内存，绝不每次命中都 UPDATE —— WAL 仍是单写者，
// 多进程共享同一个 Cache.db 时命中写会与租约、Prune 互相卡到 busy_timeout。
func (c *sqliteCache) flushHits(ctx context.Context) (err error) {
	keys, deltas, lastHits := c.hitSnapshot()
	if len(keys) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin hit flush: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback hit flush: %w", rbErr))
		}
	}()
	if execErr := execHitUpdates(ctx, tx, keys, deltas, lastHits); execErr != nil {
		err = execErr
		return err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit hit flush: %w", commitErr)
	}
	c.reconcileHits(keys, deltas)
	return nil
}

func execHitUpdates(ctx context.Context, tx *sql.Tx, keys []string, deltas []int64, lastHits []time.Time) (err error) {
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE cache SET hits = hits + ?, last_hit_at = ? WHERE capture_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare hit flush: %w", err)
	}
	defer func() {
		if closeErr := stmt.Close(); closeErr != nil && !errors.Is(closeErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("close hit stmt: %w", closeErr))
		}
	}()
	for i := range keys {
		if _, execErr := stmt.ExecContext(ctx, deltas[i], lastHits[i].Unix(), keys[i]); execErr != nil {
			return fmt.Errorf("update hit: %w", execErr)
		}
	}
	if closeErr := stmt.Close(); closeErr != nil {
		return fmt.Errorf("close hit stmt: %w", closeErr)
	}
	return nil
}

func (c *sqliteCache) hitSnapshot() (keys []string, deltas []int64, lastHits []time.Time) {
	c.hitsMu.Lock()
	defer c.hitsMu.Unlock()
	keys = make([]string, 0, len(c.hits))
	deltas = make([]int64, 0, len(c.hits))
	lastHits = make([]time.Time, 0, len(c.hits))
	for key, stat := range c.hits {
		delta := stat.total - stat.flushed
		if delta <= 0 {
			continue
		}
		keys = append(keys, key)
		deltas = append(deltas, delta)
		lastHits = append(lastHits, stat.last)
	}
	return keys, deltas, lastHits
}

func (c *sqliteCache) reconcileHits(keys []string, deltas []int64) {
	c.hitsMu.Lock()
	defer c.hitsMu.Unlock()
	for i, key := range keys {
		stat, ok := c.hits[key]
		if !ok {
			continue
		}
		stat.flushed += deltas[i]
		if stat.flushed >= stat.total {
			delete(c.hits, key)
		}
	}
}

type evictCandidate struct {
	key    string
	nbytes int64
}

func (c *sqliteCache) evict(ctx context.Context) (evicted int, err error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT capture_id, nbytes FROM cache
         ORDER BY CASE WHEN last_hit_at > 0 THEN last_hit_at ELSE created_at END ASC,
                  created_at ASC, rowid ASC`)
	if err != nil {
		return 0, fmt.Errorf("query eviction candidates: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close eviction rows: %w", closeErr))
		}
	}()
	candidates := make([]evictCandidate, 0, c.cfg.CacheMaxEntries)
	var totalBytes int64
	for rows.Next() {
		var cand evictCandidate
		if scanErr := rows.Scan(&cand.key, &cand.nbytes); scanErr != nil {
			return 0, fmt.Errorf("scan eviction candidate: %w", scanErr)
		}
		candidates = append(candidates, cand)
		totalBytes += cand.nbytes
	}
	if rowErr := rows.Err(); rowErr != nil {
		return 0, fmt.Errorf("iterate eviction candidates: %w", rowErr)
	}
	victims := c.pickVictims(candidates, totalBytes)
	if len(victims) == 0 {
		c.resetApprox(candidates, totalBytes)
		return 0, nil
	}
	if evictErr := c.deleteVictims(ctx, victims); evictErr != nil {
		return 0, evictErr
	}
	c.forgetHits(victims)
	remainingEntries := len(candidates) - len(victims)
	var remainingBytes int64
	for _, cand := range candidates {
		if !containsKey(victims, cand.key) {
			remainingBytes += cand.nbytes
		}
	}
	c.approxEntries.Store(int64(remainingEntries))
	c.approxBytes.Store(remainingBytes)
	return len(victims), nil
}

func (c *sqliteCache) pickVictims(candidates []evictCandidate, totalBytes int64) []string {
	maxEntries := c.cfg.CacheMaxEntries
	maxBytes := int64(c.cfg.CacheMaxBytesMB) << 20
	countOverflow := len(candidates) - maxEntries
	victims := make([]string, 0, max(countOverflow, 0))
	remaining := totalBytes
	for i, cand := range candidates {
		if i >= countOverflow && (maxBytes <= 0 || remaining <= maxBytes) {
			break
		}
		victims = append(victims, cand.key)
		remaining -= cand.nbytes
	}
	return victims
}

func (c *sqliteCache) deleteVictims(ctx context.Context, victims []string) (err error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin eviction: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("rollback eviction: %w", rbErr))
			}
		}
	}()
	for _, key := range victims {
		if _, execErr := tx.ExecContext(ctx,
			`DELETE FROM cache WHERE capture_id = ?`, key); execErr != nil {
			return fmt.Errorf("delete evicted key: %w", execErr)
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit eviction: %w", commitErr)
	}
	return nil
}

func (c *sqliteCache) forgetHits(victims []string) {
	c.hitsMu.Lock()
	defer c.hitsMu.Unlock()
	for _, key := range victims {
		delete(c.hits, key)
	}
}

func (c *sqliteCache) resetApprox(candidates []evictCandidate, totalBytes int64) {
	c.approxEntries.Store(int64(len(candidates)))
	c.approxBytes.Store(totalBytes)
}

func containsKey(keys []string, key string) bool {
	for _, candidate := range keys {
		if candidate == key {
			return true
		}
	}
	return false
}

type oversizeMarker struct {
	Kind     string `json:"__ds_sf"`
	Oversize bool   `json:"oversize"`
	Bytes    int    `json:"bytes"`
}

func detachedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(detached, deadline)
	}
	return context.WithCancel(detached)
}

func decodeOversize(val []byte) (int, bool) {
	var marker oversizeMarker
	if err := json.Unmarshal(val, &marker); err != nil {
		return 0, false
	}
	if marker.Kind != oversizeMarkerKind || !marker.Oversize {
		return 0, false
	}
	return marker.Bytes, true
}
