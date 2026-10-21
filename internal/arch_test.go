// 文件名: arch_test.go
// 用途: 包依赖白名单（项目内 + 外部）、依赖环、规模红线 F-S2/F-S4、工具名与注解的静态守护。
//       MUST NOT 含任何业务逻辑。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

//go:build !integration

package internal_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec" // TestSizeLimits 要跑 Scripts/CheckSize.sh
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/tools"
)

// modulePath 与 go.mod 的 module 行一致。
const modulePath = "github.com/LseKit/DeepSeekSearch"

// allowedImports 是包级的**项目内** import 白名单（与 Contract 10 §二、22 号 §2.1 三处同源）。
var allowedImports = map[string][]string{
	"internal/domain":               {}, // 零依赖
	"internal/toolschema":           {}, // 纯数据：MCP 工具 JSON Schema 字面量，零依赖
	"internal/config":               {"internal/domain"},
	"internal/logging":              {"internal/domain", "internal/config"},
	"internal/deepseek":             {"internal/domain", "internal/config"},
	"internal/resilience":           {},
	"internal/resilience/ratelimit": {"internal/domain", "internal/resilience"},
	"internal/resilience/retry":     {"internal/domain", "internal/resilience"},
	"internal/resilience/budget":    {"internal/domain", "internal/resilience"},
	"internal/resilience/circuit":   {"internal/domain", "internal/resilience"},
	"internal/resilience/cache":     {"internal/domain", "internal/resilience"},
	"internal/health":               {"internal/domain", "internal/config", "internal/deepseek", "internal/resilience", "internal/resilience/ratelimit", "internal/resilience/circuit"},
	"internal/mcputil":              {"internal/domain"},
	"internal/toolvalidate":         {"internal/domain"},
	"internal/tools":                {"internal/domain", "internal/config", "internal/deepseek", "internal/resilience", "internal/toolschema", "internal/health", "internal/mcputil", "internal/toolvalidate"},
	"internal/transport":            {"internal/domain", "internal/config", "internal/logging"},
	"cmd/deepseeksearch":            {"internal/domain", "internal/config", "internal/logging", "internal/deepseek", "internal/resilience", "internal/resilience/ratelimit", "internal/resilience/retry", "internal/resilience/budget", "internal/resilience/circuit", "internal/resilience/cache", "internal/tools", "internal/toolschema", "internal/health", "internal/mcputil", "internal/toolvalidate", "internal/transport"},
}

// allowedExternal 是包级的**标准库 + 第三方**白名单（与 Contract 10 §2.1~2.8、22 号 §2.1 同源）。
// 以 "/" 结尾的项按前缀匹配，其余按全等匹配（见 matchExternal）。
var allowedExternal = map[string][]string{
	"internal/domain": {
		"errors", "fmt", "strings", "strconv", "net/url", "net/netip", "time", "sort",
		"regexp", "unicode", "unicode/utf8", "hash/fnv", "math/rand/v2", "crypto/rand",
		"context", "encoding/json", "encoding/hex", "html", "io", "net",
	},
	"internal/config": {
		"os", "fmt", "errors", "strconv", "strings", "regexp", "time", "path/filepath", "runtime",
		"maps", "math", "net", "net/url", "reflect", "sort", "unicode/utf8",
		"github.com/BurntSushi/toml", "github.com/joho/godotenv",
	},
	"internal/logging": {
		"io", "os", "fmt", "errors", "sync", "sync/atomic", "time", "log/slog", "path/filepath",
		"compress/gzip", "sort", "strconv", "strings", "regexp", "crypto/sha256", "encoding/hex",
		"context", "encoding/json", "io/fs",
	},
	"internal/deepseek": {
		"net/http", "encoding/json", "context", "time", "io", "bytes", "fmt", "errors",
		"net/url", "net", "regexp", "strings", "unicode/utf8", "crypto/tls",
	},
	"internal/resilience": {
		"context", "time",
	},
	"internal/resilience/ratelimit": {
		"context", "fmt", "math", "sync", "sync/atomic", "time",
	},
	"internal/resilience/retry": {
		"math/rand/v2", "sync", "time",
	},
	"internal/resilience/budget": {
		"fmt", "sync", "time",
	},
	"internal/resilience/circuit": {
		"fmt", "sync", "time",
	},
	"internal/resilience/cache": {
		// 没有 golang.org/x/time/rate —— rate.Limiter 无法注入 FakeClock，令牌桶自研
		"context", "crypto/sha256", "database/sql", "encoding/hex", "encoding/json",
		"errors", "fmt", "os", "path/filepath", "strings", "sync", "sync/atomic", "time",
		"modernc.org/sqlite",
	},
	"internal/tools": {
		"context", "fmt", "errors", "strings", "sort", "time", "net/url", "net", "net/netip",
		"regexp", "log/slog", "sync", "sync/atomic", "crypto/rand", "crypto/sha256",
		"encoding/hex", "encoding/json", "unicode/utf8", "strconv",
		"github.com/modelcontextprotocol/go-sdk/mcp",
	},
	"internal/toolschema": {}, // 纯数据包，无 import
	"internal/mcputil": {
		"encoding/json", "fmt", "strings", "github.com/modelcontextprotocol/go-sdk/mcp",
	},
	"internal/toolvalidate": {"fmt", "regexp", "strings", "time"},
	"internal/health": {
		"context", "crypto/sha256", "encoding/hex", "encoding/json", "fmt", "log/slog", "time",
		"github.com/modelcontextprotocol/go-sdk/mcp",
	},
	"internal/transport": {
		"context", "net/http", "os", "fmt", "errors", "time", "log/slog", "crypto/subtle",
		"encoding/json", "net", "strings", "io", "runtime/debug", "strconv",
		"github.com/modelcontextprotocol/go-sdk/mcp",
	},
	"cmd/deepseeksearch": {
		"context", "os", "os/signal", "syscall", "fmt", "flag", "log/slog", "time",
		"time/tzdata", // 副作用导入：Windows 无系统 tzdata，缺它则启动即 exit 1
		"runtime", "io", "errors", "strings",
		"github.com/modelcontextprotocol/go-sdk/mcp",
	},
}

// contains 判定白名单里有没有这一项。不用 slices.Contains 是为了少一个 import。
func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// collectPkgs 遍历 internal/ 与 cmd/ 下的非测试 .go 文件，
// 返回「包路径 → 该包全部 import 路径」的依赖图。
// 用 go/parser 的 ImportsOnly 模式：不建类型信息、不需要模块缓存与网络。
func collectPkgs(t *testing.T, root string) map[string][]string {
	t.Helper()

	graph := make(map[string][]string)
	for _, base := range []string{"internal", "cmd"} {
		dir := filepath.Join(root, base)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s 不存在 —— root 参数错了吗？go test 的工作目录是包目录", dir)
		}
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if perr != nil {
				return perr
			}
			rel, rerr := filepath.Rel(root, filepath.Dir(path))
			if rerr != nil {
				return rerr
			}
			pkg := filepath.ToSlash(rel)
			if _, seen := graph[pkg]; !seen {
				graph[pkg] = nil // 保证「零 import 的包」也进图，否则它逃过白名单登记检查
			}
			for _, imp := range f.Imports {
				graph[pkg] = append(graph[pkg], strings.Trim(imp.Path.Value, "\""))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s 失败: %v", dir, err)
		}
	}
	return graph
}

// matchExternal 判定一个外部 import（标准库或第三方）是否被放行。
// 以 "/" 结尾的白名单项按前缀匹配（放行整个模块的子包），其余按全等匹配。
func matchExternal(allow []string, imp string) bool {
	for _, a := range allow {
		if strings.HasSuffix(a, "/") {
			if strings.HasPrefix(imp, a) {
				return true
			}
			continue
		}
		if imp == a {
			return true
		}
	}
	return false
}

// detectCycle 在**项目内**依赖图上做 DFS 找环，返回第一条环路径（闭合形式）。
// 只走项目内那一组：外部依赖不构成我们的架构环。
func detectCycle(graph map[string][]string) []string {
	const (
		white = 0 // 未访问
		gray  = 1 // 在当前 DFS 栈上
		black = 2 // 已完成
	)
	color := make(map[string]int, len(graph))
	stack := make([]string, 0, len(graph))
	var cycle []string

	var visit func(n string) bool
	visit = func(n string) bool {
		color[n] = gray
		stack = append(stack, n)
		for _, imp := range graph[n] {
			if !strings.HasPrefix(imp, modulePath+"/") {
				continue
			}
			next := strings.TrimPrefix(imp, modulePath+"/")
			switch color[next] {
			case gray:
				for i, s := range stack {
					if s == next {
						cycle = append(append([]string{}, stack[i:]...), next)
						break
					}
				}
				return true
			case white:
				if visit(next) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}

	keys := make([]string, 0, len(graph))
	for k := range graph {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 固定遍历顺序，保证报错可复现
	for _, k := range keys {
		if color[k] == white && visit(k) {
			return cycle
		}
	}
	return nil
}

// TestPackageDependencies 校验每个包的 import 都在白名单内（**两组都查**），
// 且项目内依赖图无环。
func TestPackageDependencies(t *testing.T) {
	graph := collectPkgs(t, "..") // go test 的工作目录是 GitHubSrc/internal/
	if len(graph) == 0 {
		t.Fatal("没有解析到任何包 —— 检查 root 参数，MUST NOT 让本测试空转变绿")
	}

	pkgs := make([]string, 0, len(graph))
	for k := range graph {
		pkgs = append(pkgs, k)
	}
	sort.Strings(pkgs)

	for _, pkg := range pkgs {
		allowIn, okIn := allowedImports[pkg]
		allowExt, okExt := allowedExternal[pkg]
		if !okIn || !okExt {
			t.Errorf("包 %s 未登记进白名单（新增包 MUST 先改 10 号 §二 与 22 号 §2.1，再改这里）", pkg)
			continue
		}
		internal := 0
		for _, imp := range graph[pkg] {
			if strings.HasPrefix(imp, modulePath+"/") {
				internal++
				target := strings.TrimPrefix(imp, modulePath+"/")
				if !contains(allowIn, target) {
					t.Errorf("包 %s 违规 import 项目内包 %s（白名单：%v）", pkg, target, allowIn)
				}
				continue
			}
			if !matchExternal(allowExt, imp) {
				t.Errorf("包 %s 违规 import 外部包 %s（白名单：%v）", pkg, imp, allowExt)
			}
		}
		if pkg == "internal/domain" && internal != 0 {
			t.Errorf("domain 必须零内部依赖，实际有 %d 个", internal)
		}
	}

	if c := detectCycle(graph); c != nil {
		t.Errorf("依赖环：%s", strings.Join(c, " -> "))
	}
}

// prodFile 是一个生产（非测试）.go 文件的解析结果。
type prodFile struct {
	path string
	fset *token.FileSet
	file *ast.File
	src  []string // 按行切开的源文件，用于 F-S2 的「非注释非空行」计数
}

// parseProdFiles 解析 internal/ 与 cmd/ 下全部非 _test.go 文件。
// 排除 _test.go 与 CheckSize.sh 的 find 口径一致。
func parseProdFiles(t *testing.T, root string) []prodFile {
	t.Helper()

	var out []prodFile
	for _, base := range []string{"internal", "cmd"} {
		dir := filepath.Join(root, base)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s 不存在 —— root 参数错了吗？", dir)
		}
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if perr != nil {
				return perr
			}
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			out = append(out, prodFile{
				path: filepath.ToSlash(path),
				fset: fset,
				file: f,
				src:  strings.Split(string(raw), "\n"),
			})
			return nil
		})
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", dir, err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// blockCommentLines 返回完全落在 /* */ 块注释内的行号集合。
func blockCommentLines(pf prodFile) map[int]bool {
	lines := make(map[int]bool)
	for _, cg := range pf.file.Comments {
		for _, c := range cg.List {
			if !strings.HasPrefix(c.Text, "/*") {
				continue
			}
			tf := pf.fset.File(c.Pos())
			for ln := tf.Line(c.Pos()); ln <= tf.Line(c.End()); ln++ {
				lines[ln] = true
			}
		}
	}
	return lines
}

// TestFunctionLength 强制 F-S2：单函数 ≤50 行（不含注释与空行）。
func TestFunctionLength(t *testing.T) {
	const limit = 50
	files := parseProdFiles(t, "..")
	if len(files) == 0 {
		t.Fatal("没有解析到任何生产文件")
	}
	for _, pf := range files {
		blocks := blockCommentLines(pf)
		ast.Inspect(pf.file, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				return true
			}
			tf := pf.fset.File(fd.Body.Lbrace)
			lo, hi := tf.Line(fd.Body.Lbrace)+1, tf.Line(fd.Body.Rbrace)-1
			count := 0
			for ln := lo; ln <= hi && ln <= len(pf.src); ln++ {
				s := strings.TrimSpace(pf.src[ln-1])
				if s == "" || strings.HasPrefix(s, "//") || blocks[ln] {
					continue
				}
				count++
			}
			if count <= limit {
				return true
			}
			t.Errorf("F-S2 违规: %s 的 %s 有 %d 行非注释代码（上限 %d）",
				pf.path, fd.Name.Name, count, limit)
			return true
		})
	}
}

// nestingOf 递归求最大嵌套深度。
// 口径：if / for / range / switch / type switch / select / func 字面量各 +1，
// 函数体本身是第 0 层。
func nestingOf(n ast.Node, depth int) int {
	best := depth
	ast.Inspect(n, func(c ast.Node) bool {
		if c == nil || c == n {
			return true
		}
		d := depth
		switch c.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt,
			*ast.TypeSwitchStmt, *ast.SelectStmt, *ast.FuncLit:
			d++
		}
		if v := nestingOf(c, d); v > best {
			best = v
		}
		return false
	})
	return best
}

// TestNestingDepth 强制 F-S4：嵌套 ≤3 层。
func TestNestingDepth(t *testing.T) {
	const limit = 3
	for _, pf := range parseProdFiles(t, "..") {
		ast.Inspect(pf.file, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				return true
			}
			d := nestingOf(fd.Body, 0)
			if d <= limit {
				return false
			}
			t.Errorf("F-S4 违规: %s 的 %s 嵌套 %d 层（上限 %d）",
				pf.path, fd.Name.Name, d, limit)
			return false
		})
	}
}

// TestSizeLimits 把 CheckSize.sh（F-S1/F-S3/F-S5）接进 go test。
//
// 路径口径：go test 的工作目录是包目录（GitHubSrc/internal/），
// 所以脚本在 ../Scripts/、代码根是 ..、文档根是 ../../Doc（部署根的 Doc/）。
// CI 环境脚本缺失 MUST Fatalf：静默 Skip 会让架构测试在脚本缺失时变绿。
func TestSizeLimits(t *testing.T) {
	const script = "../Scripts/CheckSize.sh"
	if _, err := os.Stat(script); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("CI 环境中 %s 不存在，规模红线无法验证: %v", script, err)
		}
		t.Skipf("本地未找到 %s（Task-11 之后才存在）；改用 20-编码规范 §3.1 的自查命令", script)
		return
	}
	out, err := exec.Command("bash", script, "..", "../../Doc").CombinedOutput()
	if err != nil {
		t.Fatalf("规模红线违规:\n%s", out)
	}
	t.Logf("CheckSize.sh 输出:\n%s", out)
}

// TestToolNames 锁死三个工具名与固定顺序。
func TestToolNames(t *testing.T) {
	got := make([]string, 0, 3)
	for _, tool := range tools.Registry() {
		got = append(got, tool.Name())
	}
	want := []string{"ds_extract", "ds_fetch", "ds_health", "ds_research", "ds_search"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("工具名 = %v, want %v", got, want)
	}
}

// TestToolAnnotations 校验三个工具都带三项注解（契约 15 §七之二）。
func TestToolAnnotations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv := mcp.NewServer(&mcp.Implementation{Name: "DeepSeekSearch", Version: "test"}, nil)
	if err := tools.RegisterAll(srv, tools.Deps{}); err != nil {
		t.Fatalf("RegisterAll 失败: %v", err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() {
		if err := srv.Run(ctx, serverTransport); err != nil && !errors.Is(err, context.Canceled) {
			return
		}
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "arch-test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect 失败: %v", err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Errorf("session.Close() = %v", closeErr)
		}
	}()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools 失败: %v", err)
	}
	if len(res.Tools) != 5 {
		t.Fatalf("tools/list 返回 %d 个工具, want 5", len(res.Tools))
	}
	for _, tool := range res.Tools {
		if tool.Annotations == nil {
			t.Errorf("%s 缺少 annotations", tool.Name)
			continue
		}
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s.readOnlyHint = false, want true", tool.Name)
		}
		if !tool.Annotations.IdempotentHint {
			t.Errorf("%s.idempotentHint = false, want true", tool.Name)
		}
		if tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
			t.Errorf("%s.openWorldHint 必须显式为 true", tool.Name)
		}
	}
}
