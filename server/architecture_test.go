// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package main

// Architecture rules from CONTRIBUTING.md ("Code style") checked on the source itself.
// The baselines only ever go down: go test -run Architecture -update . rewrites them after an improvement.

import (
	"bufio"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	maxFunctionLines  = 80
	maxAddonFileLines = 350
	baselineDir       = "testdata/architecture"
)

// boundaryFiles are the only files of a domain package where untyped dump maps belong:
// read.go turns dump values into the package's types, render.go turns results into the answer.
var boundaryFiles = map[string]bool{"read.go": true, "render.go": true}

var forbiddenDomainImports = map[string]string{
	"os": "file system", "io/fs": "file system", "path/filepath": "file system",
	"net": "network", "net/http": "network", "os/exec": "processes", "log": "logging belongs to the adapters",
}

type sourceFile struct {
	path string
	fset *token.FileSet
	ast  *ast.File
}

func productionFiles(t *testing.T, root string) []sourceFile {
	t.Helper()
	var files []sourceFile
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		if strings.Contains(filepath.ToSlash(path), "internal/fixtures/") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		files = append(files, sourceFile{filepath.ToSlash(path), fset, parsed})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestArchitectureDomainImports(t *testing.T) {
	for _, f := range productionFiles(t, "internal/game") {
		for _, spec := range f.ast.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			switch {
			case forbiddenDomainImports[path] != "":
				t.Errorf("%s imports %q: no %s in the domain", f.path, path, forbiddenDomainImports[path])
			case !strings.Contains(path, "."): // standard library
			case path == "wowmcp/internal/snapshot" || strings.HasPrefix(path, "wowmcp/internal/game/"):
			case strings.HasPrefix(path, "wowmcp/"):
				t.Errorf("%s imports %q: a domain package may import only snapshot and other game packages", f.path, path)
			default:
				t.Errorf("%s imports %q: no third-party code in the domain", f.path, path)
			}
		}
	}
}

func isUntypedMap(n ast.Node) bool {
	m, ok := n.(*ast.MapType)
	if !ok {
		return false
	}
	key, _ := m.Key.(*ast.Ident)
	switch value := m.Value.(type) {
	case *ast.Ident:
		return key != nil && key.Name == "string" && value.Name == "any"
	case *ast.InterfaceType:
		return key != nil && key.Name == "string" && len(value.Methods.List) == 0
	}
	return false
}

// TestArchitectureUntypedMaps keeps map[string]any out of the domain rules: per package, the number of
// untyped maps outside read.go and render.go may never grow.
func TestArchitectureUntypedMaps(t *testing.T) {
	counts := map[string]int{}
	for _, f := range productionFiles(t, "internal/game") {
		pkg := filepath.Base(filepath.Dir(f.path))
		if _, seen := counts[pkg]; !seen {
			counts[pkg] = 0
		}
		if boundaryFiles[filepath.Base(f.path)] {
			continue
		}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			if isUntypedMap(n) {
				counts[pkg]++
			}
			return true
		})
	}
	checkBaseline(t, "untyped_maps.json", counts,
		"use the package's types; map[string]any belongs only in read.go (dump -> types) and render.go (result -> answer)")
}

// TestArchitectureOpaqueValues: a rule file may carry a dump value it only passes through as `any`
// (a name the client may not have sent), but the number of such values per package may never grow.
func TestArchitectureOpaqueValues(t *testing.T) {
	counts := map[string]int{}
	for _, f := range productionFiles(t, "internal/game") {
		if boundaryFiles[filepath.Base(f.path)] {
			continue
		}
		pkg := filepath.Base(filepath.Dir(f.path))
		ast.Inspect(f.ast, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == "any" {
				counts[pkg]++
			}
			return true
		})
	}
	checkBaseline(t, "opaque_values.json", counts,
		"give the value a type in read.go instead of passing `any` through the rules")
}

// TestArchitectureFunctionLength: no new function over maxFunctionLines; the listed ones may only shrink.
func TestArchitectureFunctionLength(t *testing.T) {
	long := map[string]int{}
	for _, f := range productionFiles(t, ".") {
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			lines := f.fset.Position(fn.End()).Line - f.fset.Position(fn.Pos()).Line + 1
			if lines > maxFunctionLines {
				long[f.path+" "+funcName(fn)] = lines
			}
		}
	}
	checkBaseline(t, "long_functions.json", long,
		"split the function: the limit is "+strconv.Itoa(maxFunctionLines)+" lines")
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if id, ok := recv.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// TestArchitectureAddonFileSize: no addon file over maxAddonFileLines. The baseline exists only for
// vendored libraries (LibDeflate.lua) that cannot be split; our own files never enter it.
func TestArchitectureAddonFileSize(t *testing.T) {
	root := filepath.Join("..", "addon", "WowMcp")
	long := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".lua") {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		lines := 0
		for scanner := bufio.NewScanner(file); scanner.Scan(); {
			lines++
		}
		if lines > maxAddonFileLines {
			long[filepath.ToSlash(path)] = lines
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	checkBaseline(t, "addon_file_size.json", long,
		"give each responsibility its own file: the limit is "+strconv.Itoa(maxAddonFileLines)+" lines (only vendored libraries may stay in the baseline)")
}

// checkBaseline fails when a value is above its baseline (a missing entry counts as zero) and, so that
// an improvement cannot be silently lost again, also when the baseline is higher than the code needs.
func checkBaseline(t *testing.T, name string, current map[string]int, advice string) {
	t.Helper()
	path := filepath.Join(baselineDir, name)
	baseline := map[string]int{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && *updateGolden {
		writeBaseline(t, path, current)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	keys := map[string]bool{}
	for k := range current {
		keys[k] = true
	}
	for k := range baseline {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	worse, better := false, false
	for _, k := range sorted {
		switch now, allowed := current[k], baseline[k]; {
		case now > allowed:
			worse = true
			t.Errorf("%s: %d, allowed %d: %s", k, now, allowed, advice)
		case now < allowed:
			better = true
			if !*updateGolden {
				t.Errorf("%s: %d, baseline still says %d: run go test -run Architecture -update . and commit %s", k, now, allowed, path)
			}
		}
	}
	if *updateGolden && better && !worse {
		writeBaseline(t, path, current)
	}
}

func writeBaseline(t *testing.T, path string, current map[string]int) {
	t.Helper()
	kept := map[string]int{}
	for k, v := range current {
		if v > 0 {
			kept[k] = v
		}
	}
	data, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(baselineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
