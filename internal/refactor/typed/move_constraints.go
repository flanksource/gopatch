package typed

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type moveFileContext struct {
	test          bool
	suffix, build string
}

func compatibleMoveFiles(sources []string, destination string, destinationExists, preserveBuildComment bool) error {
	if len(sources) == 0 {
		return nil
	}
	want, err := readMoveFileContext(sources[0], true)
	if err != nil {
		return err
	}
	if preserveBuildComment {
		want.build = ""
	}
	for _, source := range sources[1:] {
		context, err := readMoveFileContext(source, true)
		if err != nil {
			return err
		}
		if preserveBuildComment {
			context.build = ""
		}
		if context != want {
			return fmt.Errorf("moved declarations come from files with incompatible build constraints: %v and %v", sources[0], source)
		}
	}
	got, err := readMoveFileContext(destination, destinationExists)
	if err != nil {
		return err
	}
	if preserveBuildComment {
		got.build = ""
	}
	if want != got {
		return fmt.Errorf("source and destination have incompatible build constraints or test-file classification: %v -> %v", sources[0], destination)
	}
	return nil
}

func importsC(file *ast.File) bool {
	if file == nil {
		return false
	}
	for _, spec := range file.Imports {
		if path, err := strconv.Unquote(spec.Path.Value); err == nil && path == "C" {
			return true
		}
	}
	return false
}

func hasInitFunction(file *ast.File) bool {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "init" {
			return true
		}
	}
	return false
}

func readMoveFileContext(path string, exists bool) (moveFileContext, error) {
	context := moveFileContext{
		test:   strings.HasSuffix(path, "_test.go"),
		suffix: constrainedFileSuffix(filepath.Base(path)),
	}
	if !exists {
		return context, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return context, err
	}
	for _, line := range strings.Split(string(contents), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "//go:build "), strings.HasPrefix(trimmed, "// +build "):
			context.build += trimmed + "\n"
		case strings.HasPrefix(trimmed, "package "):
			return context, nil
		}
	}
	return context, nil
}

func constrainedFileSuffix(name string) string {
	name = strings.TrimSuffix(name, ".go")
	name = strings.TrimSuffix(name, "_test")
	parts := strings.Split(name, "_")
	if len(parts) < 2 {
		return ""
	}
	last := parts[len(parts)-1]
	if knownGoArch[last] {
		if len(parts) > 2 && knownGoOS[parts[len(parts)-2]] {
			return parts[len(parts)-2] + "_" + last
		}
		return last
	}
	if knownGoOS[last] {
		return last
	}
	return ""
}

var knownGoOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "hurd": true, "illumos": true, "ios": true,
	"js": true, "linux": true, "nacl": true, "netbsd": true,
	"openbsd": true, "plan9": true, "solaris": true, "wasip1": true,
	"windows": true, "zos": true,
}

var knownGoArch = map[string]bool{
	"386": true, "amd64": true, "amd64p32": true, "arm": true,
	"arm64": true, "arm64be": true, "armbe": true, "loong64": true,
	"mips": true, "mips64": true, "mips64le": true, "mips64p32": true,
	"mips64p32le": true, "mipsle": true, "ppc": true, "ppc64": true,
	"ppc64le": true, "riscv": true, "riscv64": true, "s390": true,
	"s390x": true, "sparc": true, "sparc64": true, "wasm": true,
}
