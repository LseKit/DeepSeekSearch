// 文件名: exportcount.go
// 用途: 供 CheckSize.sh 调用的精确 F-S5 计数器：按包输出非测试文件数 / 物理行数 / 导出符号数。
//       导出符号按 go/ast 统计（顶层 func/type/var/const、方法、const/var/type 块成员），
//       MUST NOT 把 struct 字段算进去。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

//go:build ignore

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	dirs, err := filepath.Glob(filepath.Join(root, "internal", "*"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		info, statErr := os.Stat(dir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		files, lines, exports, countErr := countPackage(dir)
		if countErr != nil {
			fmt.Fprintln(os.Stderr, countErr)
			os.Exit(1)
		}
		rel, relErr := filepath.Rel(root, dir)
		if relErr != nil {
			fmt.Fprintln(os.Stderr, relErr)
			os.Exit(1)
		}
		fmt.Printf("%s %d %d %d\n", filepath.ToSlash(rel), files, lines, exports)
	}
}

func countPackage(dir string) (files, lines, exports int, err error) {
	fset := token.NewFileSet()
	names := make(map[string]bool)
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files++
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lines += strings.Count(string(raw), "\n")
		f, parseErr := parser.ParseFile(fset, path, raw, 0)
		if parseErr != nil {
			return parseErr
		}
		collectExports(f, names)
		return nil
	})
	return files, lines, len(names), walkErr
}

func collectExports(f *ast.File, names map[string]bool) {
	for _, decl := range f.Decls {
		switch dd := decl.(type) {
		case *ast.FuncDecl:
			if ast.IsExported(dd.Name.Name) {
				names[dd.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range dd.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if ast.IsExported(s.Name.Name) {
						names[s.Name.Name] = true
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if ast.IsExported(n.Name) {
							names[n.Name] = true
						}
					}
				}
			}
		}
	}
}
