// 文件名: rotate.go
// 用途: 日志文件的命名解析、保留期清理、备份数兜底与旧文件 gzip。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package logging

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// logNameRe 从文件名解析日期、时间、PID 与可选撞名序号。
// 文件名固定为 <prefix>_YYYYMMDD_HHMMSS_<PID>[_<n>].log[.gz]；
// 不用文件名排序推断日期 —— prefix 变了排序就失效。
var logNameRe = regexp.MustCompile(`^(.+)_(\d{8})_(\d{6})_(\d+)(?:_(\d+))?\.log(?:\.gz)?$`)

type parsedLogName struct {
	prefix     string
	name       string
	date       time.Time
	pid        int
	seq        int
	compressed bool
}

func parseLogName(name string, loc *time.Location) (parsedLogName, bool) {
	match := logNameRe.FindStringSubmatch(name)
	if match == nil {
		return parsedLogName{}, false
	}
	if loc == nil {
		loc = time.UTC
	}
	date, err := time.ParseInLocation("20060102_150405", match[2]+"_"+match[3], loc)
	if err != nil {
		return parsedLogName{}, false
	}
	pid, err := strconv.Atoi(match[4])
	if err != nil {
		return parsedLogName{}, false
	}
	seq := 0
	if match[5] != "" {
		seq, err = strconv.Atoi(match[5])
		if err != nil {
			return parsedLogName{}, false
		}
	}
	return parsedLogName{
		prefix:     match[1],
		name:       name,
		date:       date,
		pid:        pid,
		seq:        seq,
		compressed: strings.HasSuffix(name, ".gz"),
	}, true
}

// pruneByAge 删除创建日期早于 today-(maxAge-1) 的旧文件，并把旧 .log 压缩成 .log.gz。
// 「含今天」：maxAge=7 时保留今天与之前 6 天，共 7 个自然日。
// today 的日期文件永不删除、永不压缩；maxBackups 只约束旧文件。
func pruneByAge(dir, prefix string, today time.Time, maxAge, maxBackups int, compress bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("读取日志目录失败: %w", err)
	}
	if maxAge < 1 {
		maxAge = 1
	}
	if maxBackups < 0 {
		maxBackups = 0
	}
	todayDate := dateOnly(today)
	cutoff := todayDate.AddDate(0, 0, -(maxAge - 1))
	kept, removed, errs := removeOldFiles(dir, selectOldFiles(entries, prefix, todayDate), cutoff)
	kept, trimmed, trimErrs := trimAndCompress(dir, kept, maxBackups, compress)
	return removed + trimmed, errors.Join(append(errs, trimErrs...)...)
}

// selectOldFiles 选出非今天、同前缀的日志文件，并按日期升序。
func selectOldFiles(entries []fs.DirEntry, prefix string, todayDate time.Time) []parsedLogName {
	oldFiles := make([]parsedLogName, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		parsed, ok := parseLogName(entry.Name(), todayDate.Location())
		if !ok || parsed.prefix != prefix || sameDate(parsed.date, todayDate) {
			continue
		}
		oldFiles = append(oldFiles, parsed)
	}
	sort.Slice(oldFiles, func(i, j int) bool { return oldFiles[i].date.Before(oldFiles[j].date) })
	return oldFiles
}

// removeOldFiles 删除早于 cutoff 的文件，返回保留项。
func removeOldFiles(dir string, files []parsedLogName, cutoff time.Time) ([]parsedLogName, int, []error) {
	kept := make([]parsedLogName, 0, len(files))
	removed := 0
	var errs []error
	for _, file := range files {
		if file.date.Before(cutoff) {
			if err := removeIfExists(filepath.Join(dir, file.name)); err != nil {
				errs = append(errs, err)
				continue
			}
			removed++
			continue
		}
		kept = append(kept, file)
	}
	return kept, removed, errs
}

// trimAndCompress 执行 maxBackups 兜底与压缩。
func trimAndCompress(dir string, kept []parsedLogName, maxBackups int, compress bool) ([]parsedLogName, int, []error) {
	removed := 0
	var errs []error
	if len(kept) > maxBackups {
		drop := len(kept) - maxBackups
		for _, file := range kept[:drop] {
			if err := removeIfExists(filepath.Join(dir, file.name)); err != nil {
				errs = append(errs, err)
				continue
			}
			removed++
		}
		kept = kept[drop:]
	}
	if compress {
		for _, file := range kept {
			if file.compressed {
				continue
			}
			if err := gzipFile(filepath.Join(dir, file.name)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return kept, removed, errs
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func sameDate(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("删除 %s 失败: %w", path, err)
}

// gzipFile 把 path 压缩为 path.gz 后删除原文件。
// 已存在 .gz（其他进程压过）时只删原文件，避免重复压缩。
func gzipFile(path string) (err error) {
	if _, statErr := os.Stat(path + ".gz"); statErr == nil {
		return removeIfExists(path)
	}

	src, openErr := os.Open(path) //nolint:gosec // 路径来自日志目录内的已解析文件名
	if openErr != nil {
		if errors.Is(openErr, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("打开待压缩日志失败: %w", openErr)
	}
	defer func() { err = errors.Join(err, src.Close()) }()

	tmp := path + ".gz.tmp"
	dst, createErr := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // 路径来自日志目录内的已解析文件名
	if createErr != nil {
		return fmt.Errorf("创建压缩临时文件失败: %w", createErr)
	}
	zw := gzip.NewWriter(dst)
	if _, copyErr := io.Copy(zw, src); copyErr != nil {
		return errors.Join(fmt.Errorf("压缩日志失败: %w", copyErr),
			zw.Close(), dst.Close(), removeIfExists(tmp))
	}
	if closeErr := zw.Close(); closeErr != nil {
		return errors.Join(fmt.Errorf("关闭 gzip 失败: %w", closeErr),
			dst.Close(), removeIfExists(tmp))
	}
	if closeErr := dst.Close(); closeErr != nil {
		return errors.Join(fmt.Errorf("关闭压缩文件失败: %w", closeErr), removeIfExists(tmp))
	}
	if renameErr := os.Rename(tmp, path+".gz"); renameErr != nil {
		return errors.Join(fmt.Errorf("重命名压缩文件失败: %w", renameErr), removeIfExists(tmp))
	}
	return removeIfExists(path)
}
