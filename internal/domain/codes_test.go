// 文件名: codes_test.go
// 用途: 校验 42 个错误码的数量、命名、分段与可重试标志。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"fmt"
	"reflect"
	"regexp"
	"testing"
)

func TestCodeRegistry(t *testing.T) {
	t.Parallel()

	if got, want := len(CodeRegistry), 42; got != want {
		t.Fatalf("CodeRegistry 长度 = %d, want %d（不许增删而不改文档）", got, want)
	}

	nameRe := regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	for code, info := range CodeRegistry {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			t.Parallel()

			if !nameRe.MatchString(info.Name) {
				t.Errorf("code %d 的 Name %q 不是 UPPER_SNAKE", code, info.Name)
			}
			if info.Message == "" || info.Hint == "" {
				t.Errorf("code %d 的 Message/Hint 为空", code)
			}
			// 2xxx 必须是 2000 + http_status；2000/2998/2999 是无状态码的哨兵。
			if code >= 2000 && code < 3000 && code != 2000 && code != 2998 && code != 2999 {
				status := code - 2000
				if status < 400 || status > 599 {
					t.Errorf("code %d 推出的 http_status %d 不合法", code, status)
				}
			}
		})
	}

	if info, ok := CodeRegistry[CodeValSyntaxConflict]; !ok {
		t.Error("1080 软错误 MUST 在 CodeRegistry 内")
	} else if info.Name != "VAL_SYNTAX_CONFLICT" {
		t.Errorf("1080 的 Name = %q, want VAL_SYNTAX_CONFLICT", info.Name)
	}
}

func TestCodeInfoFields(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(CodeInfo{})
	if got, want := typ.NumField(), 4; got != want {
		t.Fatalf("CodeInfo 字段数 = %d, want %d", got, want)
	}
	wantNames := []string{"Name", "Message", "Hint", "Retryable"}
	for i, want := range wantNames {
		if got := typ.Field(i).Name; got != want {
			t.Errorf("CodeInfo 第 %d 个字段 = %s, want %s", i+1, got, want)
		}
	}
}

func TestCodeRegistryTextLength(t *testing.T) {
	t.Parallel()

	for code, info := range CodeRegistry {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			t.Parallel()

			if n := len([]rune(info.Message)); n > 60 {
				t.Errorf("code %d 的 Message 有 %d rune，超过 60", code, n)
			}
			if n := len([]rune(info.Hint)); n > 120 {
				t.Errorf("code %d 的 Hint 有 %d rune，超过 120", code, n)
			}
		})
	}
}

func TestCodeRegistrySegments(t *testing.T) {
	t.Parallel()

	counts := map[string]int{}
	for code := range CodeRegistry {
		switch {
		case code >= 1000 && code < 2000:
			counts["1xxx"]++
		case code >= 2000 && code < 3000:
			counts["2xxx"]++
		case code >= 3000 && code < 4000:
			counts["3xxx"]++
		default:
			t.Errorf("code %d 不在 1xxx~3xxx 分段内", code)
		}
	}

	tests := []struct {
		seg  string
		want int
	}{
		{"1xxx", 11},
		{"2xxx", 14},
		{"3xxx", 17},
	}
	for _, tt := range tests {
		t.Run(tt.seg, func(t *testing.T) {
			t.Parallel()
			if got := counts[tt.seg]; got != tt.want {
				t.Errorf("%s 段数量 = %d, want %d", tt.seg, got, tt.want)
			}
		})
	}
}

func TestCodeConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code int
		name string
	}{
		{CodeValInvalidParam, "VAL_INVALID_PARAM"},
		{CodeValQueryTooLong, "VAL_QUERY_TOO_LONG"},
		{CodeValCountRange, "VAL_COUNT_RANGE"},
		{CodeValDateFormat, "VAL_DATE_FORMAT"},
		{CodeValURLScheme, "VAL_URL_SCHEME"},
		{CodeValURLCredentials, "VAL_URL_CREDENTIALS"},
		{CodeValURLTooLong, "VAL_URL_TOO_LONG"},
		{CodeValSSRFBlocked, "VAL_SSRF_BLOCKED"},
		{CodeValSyntaxConflict, "VAL_SYNTAX_CONFLICT"},
		{CodeValSectionNotFound, "VAL_SECTION_NOT_FOUND"},
		{CodeValSynthDisabled, "VAL_SYNTH_DISABLED"},
		{CodeUpValidation, "UP_VALIDATION"},
		{CodeUpAuth, "UP_AUTH"},
		{CodeUpBalance, "UP_BALANCE"},
		{CodeUpForbidden, "UP_FORBIDDEN"},
		{CodeUpNotFound, "UP_NOT_FOUND"},
		{CodeUpTimeout, "UP_TIMEOUT"},
		{CodeUpTooLarge, "UP_TOO_LARGE"},
		{CodeUpRateLimited, "UP_RATE_LIMITED"},
		{CodeUpServer, "UP_SERVER"},
		{CodeUpUnavailable, "UP_UNAVAILABLE"},
		{CodeUpGatewayTimeout, "UP_GATEWAY_TIMEOUT"},
		{CodeUpUnexpected, "UP_UNEXPECTED"},
		{CodeUpNetwork, "UP_NETWORK"},
		{CodeUpParse, "UP_PARSE"},
		{CodeLocQueueTimeout, "LOC_QUEUE_TIMEOUT"},
		{CodeLocRetryBudget, "LOC_RETRY_BUDGET"},
		{CodeLocTotalTimeout, "LOC_TOTAL_TIMEOUT"},
		{CodeLocCircuitOpen, "LOC_CIRCUIT_OPEN"},
		{CodeLocNoAPIKey, "LOC_NO_API_KEY"},
		{CodeLocHTTPTokenMissing, "LOC_HTTP_TOKEN_MISSING"},
		{CodeLocContentTooShort, "LOC_CONTENT_TOO_SHORT"},
		{CodeLocCacheDegraded, "LOC_CACHE_DEGRADED"},
		{CodeLocBudgetExceeded, "LOC_BUDGET_EXCEEDED"},
		{CodeLocSingleflightTimeout, "LOC_SINGLEFLIGHT_TIMEOUT"},
		{CodeLocSnapshotGone, "LOC_SNAPSHOT_GONE"},
		{CodeLocConfigInvalid, "LOC_CONFIG_INVALID"},
		{CodeLocHTTPUnauthorized, "LOC_HTTP_UNAUTHORIZED"},
		{CodeLocHTTPExposedNoAuth, "LOC_HTTP_EXPOSED_NO_AUTH"},
		{CodeLocHTTPHostRejected, "LOC_HTTP_HOST_REJECTED"},
		{CodeLocHTTPOverloaded, "LOC_HTTP_OVERLOADED"},
		{CodeLocInternal, "LOC_INTERNAL"},
	}

	if got, want := len(tests), 42; got != want {
		t.Fatalf("常量清单 = %d 条, want %d", got, want)
	}

	seen := make(map[int]string, len(tests))
	for _, tt := range tests {
		if prev, ok := seen[tt.code]; ok {
			t.Errorf("码 %d 重复：%s 与 %s", tt.code, prev, tt.name)
			continue
		}
		seen[tt.code] = tt.name

		info, ok := CodeRegistry[tt.code]
		if !ok {
			t.Errorf("CodeRegistry 缺少 code %d (%s)", tt.code, tt.name)
			continue
		}
		if info.Name != tt.name {
			t.Errorf("code %d 的 Name = %q, want %q", tt.code, info.Name, tt.name)
		}
	}

	if got, want := len(seen), len(CodeRegistry); got != want {
		t.Errorf("常量去重后 %d 个, CodeRegistry %d 条，必须相等", got, want)
	}
}

func TestCodeRegistryRetryable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code int
		want bool
	}{
		{"408 上游读体超时可重试", CodeUpTimeout, true},
		{"429 可重试", CodeUpRateLimited, true},
		{"500 可重试", CodeUpServer, true},
		{"503 可重试", CodeUpUnavailable, true},
		{"504 可重试", CodeUpGatewayTimeout, true},
		{"2998 网络错误可重试", CodeUpNetwork, true},
		{"3000 排队超时可重试", CodeLocQueueTimeout, true},
		{"3030 熔断打开可重试", CodeLocCircuitOpen, true},
		{"3075 等待填充超时可重试", CodeLocSingleflightTimeout, true},
		{"400 不可重试", CodeUpValidation, false},
		{"401 不可重试", CodeUpAuth, false},
		{"402 余额不足绝不可重试", CodeUpBalance, false},
		{"404 不可重试（实测 403 也塌缩成 404，重试只会付费）", CodeUpNotFound, false},
		{"413 不可重试", CodeUpTooLarge, false},
		{"2000 不可重试", CodeUpUnexpected, false},
		{"2999 不可重试", CodeUpParse, false},
		{"1010 不可重试", CodeValQueryTooLong, false},
		{"1080 软错误不可重试", CodeValSyntaxConflict, false},
		{"3099 不可重试", CodeLocInternal, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CodeRegistry[tt.code].Retryable; got != tt.want {
				t.Errorf("code %d Retryable = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}
