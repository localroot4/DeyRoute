package exec

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNoOtherExec is the CI guard of section 15: only internal/exec may start
// programs. It parses every Go file of the module and fails when a file
// outside internal/exec imports os/exec, calls syscall/unix Exec, ForkExec or
// StartProcess, or os.StartProcess, and when production code calls a runner
// with a literal program name that is not allow-listed.
func TestNoOtherExec(t *testing.T) {
	root := moduleRoot(t)
	violations, parsed := scanModule(t, root)
	require.Greater(t, parsed, 10, "scanner found suspiciously few Go files under %s", root)
	require.Empty(t, violations, "forbidden process execution outside internal/exec:\n%s", strings.Join(violations, "\n"))
}

// TestScannerDetectsViolations proves the guard works on a synthetic module.
func TestScannerDetectsViolations(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(src), 0o600))
	}
	write("go.mod", "module example.com/m\n")
	write("internal/exec/ok.go", "package exec\nimport osexec \"os/exec\"\nvar _ = osexec.Command\n")
	write("internal/exec/ok_test.go", "package exec\nimport \"os/exec\"\nvar _ = exec.Command\n")
	write("internal/foo/bad.go", "package foo\nimport \"os/exec\"\nvar _ = exec.Command\n")
	write("internal/foo/bad_test.go", "package foo\nimport x \"os/exec\"\nvar _ = x.Command\n")
	write("internal/bar/sys.go", `package bar
import (
	sc "syscall"
	"golang.org/x/sys/unix"
	"os"
)
func f() {
	_ = sc.Exec("/bin/sh", nil, nil)
	_, _ = sc.ForkExec("/bin/sh", nil, nil)
	_ = unix.Exec("/bin/sh", nil, nil)
	_, _ = os.StartProcess("/bin/sh", nil, nil)
	_ = sc.Getpid()
}
`)
	write("internal/baz/run.go", `package baz
func g(r interface{ Run(a any, b string, c []string, d []byte) }) {
	r.Run(nil, "wg", nil, nil)
	r.Run(nil, "systemctl", nil, nil)
	r.Run(nil, "/usr/bin/curl", nil, nil)
}
`)
	write("internal/baz/run_test.go", "package baz\nfunc h(r interface{ Run(a any, b string, c []string, d []byte) }) { r.Run(nil, \"bash\", nil, nil) }\n")
	write("internal/dot/dot.go", "package dot\nimport . \"syscall\"\nvar _ = Getpid\n")
	write("internal/broken/broken.go", "package broken\nimport \"os/exec\"\nfunc {\n")
	write("testdata/skip.go", "package x\nimport \"os/exec\"\n")
	write(".hidden/skip.go", "package x\nimport \"os/exec\"\n")
	write("vendor/v/skip.go", "package v\nimport \"os/exec\"\n")
	write("dist/skip.go", "package d\nimport \"os/exec\"\n")
	// Bypasses: the execabs wrapper, raw exec/fork syscalls and program
	// names held in constants (declared in another file of the package).
	write("internal/abs/abs.go", "package abs\nimport ea \"golang.org/x/sys/execabs\"\nvar _ = ea.Command\n")
	write("internal/raw/raw.go", `package raw
import (
	"syscall"
	"golang.org/x/sys/unix"
)
func f() {
	syscall.RawSyscall(syscall.SYS_EXECVE, 0, 0, 0)
	unix.Syscall(unix.SYS_CLONE3, 0, 0, 0)
	_ = syscall.SYS_GETPID
}
`)
	write("internal/konst/names.go", "package konst\nconst shell = \"bash\"\nconst ok = \"nft\"\n")
	write("internal/konst/run.go", `package konst
func g(r interface{ Run(a any, b string, c []string, d []byte) }) {
	r.Run(nil, shell, nil, nil)
	r.Run(nil, (ok), nil, nil)
	r.Run(nil, unknownVar, nil, nil)
}
`)
	// A nested directory named dist is compiled and therefore scanned.
	write("internal/dist/nested.go", "package dist\nimport \"os/exec\"\nvar _ = exec.Command\n")

	violations, parsed := scanModule(t, root)
	sort.Strings(violations)
	joined := strings.Join(violations, "\n")
	require.Equal(t, 14, parsed)
	for _, want := range []string{
		"internal/foo/bad.go: imports os/exec",
		"internal/foo/bad_test.go: imports os/exec",
		"internal/bar/sys.go:8: calls syscall.Exec",
		"internal/bar/sys.go:9: calls syscall.ForkExec",
		"internal/bar/sys.go:10: calls golang.org/x/sys/unix.Exec",
		"internal/bar/sys.go:11: calls os.StartProcess",
		`internal/baz/run.go:3: runs "wg"`,
		`internal/baz/run.go:5: runs "/usr/bin/curl"`,
		"internal/dot/dot.go: dot-imports syscall",
		"internal/broken/broken.go: imports os/exec",
		"internal/abs/abs.go: imports golang.org/x/sys/execabs",
		"internal/raw/raw.go:7: calls syscall.SYS_EXECVE",
		"internal/raw/raw.go:8: calls golang.org/x/sys/unix.SYS_CLONE3",
		`internal/konst/run.go:3: runs "bash"`,
		"internal/dist/nested.go: imports os/exec",
	} {
		require.Contains(t, joined, want)
	}
	require.NotContains(t, joined, "internal/exec/ok")
	require.NotContains(t, joined, `"systemctl"`)
	require.NotContains(t, joined, `"nft"`)
	require.NotContains(t, joined, "run_test.go")
	require.NotContains(t, joined, "Getpid")
	require.NotContains(t, joined, "SYS_GETPID")
	require.NotContains(t, joined, "skip.go")
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "go.mod not found above %s", file)
		dir = parent
	}
}

// forbiddenImports may only be imported by internal/exec: os/exec and
// golang.org/x/sys/execabs (a thin os/exec wrapper shipped in the allowed
// x/sys module).
var forbiddenImports = map[string]bool{
	"os/exec":                  true,
	"golang.org/x/sys/execabs": true,
}

// processSyscalls are the raw syscall numbers that replace or clone the
// process image (usable through Syscall/RawSyscall).
var processSyscalls = map[string]bool{
	"SYS_EXECVE": true, "SYS_EXECVEAT": true, "SYS_FORK": true, "SYS_VFORK": true,
	"SYS_CLONE": true, "SYS_CLONE3": true,
}

// guarded maps an import path to the selectors that start processes.
var guarded = map[string]map[string]bool{
	"syscall":               withProcessSyscalls("Exec", "ForkExec", "StartProcess"),
	"golang.org/x/sys/unix": withProcessSyscalls("Exec", "ForkExec", "StartProcess"),
	"os":                    {"StartProcess": true},
}

func withProcessSyscalls(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	for n := range processSyscalls {
		m[n] = true
	}
	return m
}

// goFile is one parsed file of the module.
type goFile struct {
	fset   *token.FileSet
	f      *ast.File
	rel    string
	dir    string
	inExec bool
	isTest bool
}

// scanModule returns violations as "relpath[:line]: what" and the number of
// Go files parsed. Directories the go tool never compiles (".x", "_x",
// testdata, vendor) and the top-level dist/ (release binaries) are skipped.
func scanModule(t *testing.T, root string) (violations []string, parsed int) {
	t.Helper()
	execDir := filepath.Join(root, "internal", "exec")
	var files []goFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
				name == "testdata" || name == "vendor" || name == "node_modules" ||
				path == filepath.Join(root, "dist")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if f == nil {
			t.Logf("skipping unparsable %s: %v", rel, perr)
			return nil
		}
		files = append(files, goFile{
			fset: fset, f: f, rel: filepath.ToSlash(rel), dir: filepath.Dir(path),
			inExec: filepath.Dir(path) == execDir, isTest: strings.HasSuffix(name, "_test.go"),
		})
		return nil
	})
	require.NoError(t, err)

	// String constants per directory, so Run(ctx, prog, …) with a named
	// constant is checked like a literal.
	consts := map[string]map[string]string{}
	for _, gf := range files {
		if consts[gf.dir] == nil {
			consts[gf.dir] = map[string]string{}
		}
		collectStringConsts(gf.f, consts[gf.dir])
	}
	for _, gf := range files {
		violations = append(violations, checkFile(gf.fset, gf.f, gf.rel, gf.inExec, gf.isTest, consts[gf.dir])...)
	}
	return violations, len(files)
}

// collectStringConsts records every package-level `const name = "literal"`
// of f (function-local constants are left out: without type checking a
// local variable of the same name elsewhere would be misread as them).
func collectStringConsts(f *ast.File, into map[string]string) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, id := range vs.Names {
				if i >= len(vs.Values) {
					break
				}
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						into[id.Name] = s
					}
				}
			}
		}
	}
}

// programName returns the program a Run argument names when it is a string
// literal or a string constant of the same package.
func programName(e ast.Expr, consts map[string]string) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.Ident:
		s, ok := consts[x.Name]
		return s, ok
	case *ast.ParenExpr:
		return programName(x.X, consts)
	}
	return "", false
}

func checkFile(fset *token.FileSet, f *ast.File, rel string, inExec, isTest bool, consts map[string]string) []string {
	var out []string
	locals := map[string]string{} // local package name → guarded import path
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if forbiddenImports[p] && !inExec {
			out = append(out, rel+": imports "+p)
		}
		if _, ok := guarded[p]; !ok {
			continue
		}
		local := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		switch local {
		case "_":
		case ".":
			if !inExec {
				out = append(out, rel+": dot-imports "+p)
			}
		default:
			locals[local] = p
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			// The exec package itself may start processes; its literal
			// program names are still checked below.
			id, ok := x.X.(*ast.Ident)
			if !ok || inExec {
				return true
			}
			if p, ok := locals[id.Name]; ok && guarded[p][x.Sel.Name] {
				out = append(out, fmt.Sprintf("%s:%d: calls %s.%s", rel, fset.Position(x.Pos()).Line, p, x.Sel.Name))
			}
		case *ast.CallExpr:
			if isTest {
				return true
			}
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Run" || len(x.Args) != 4 {
				return true
			}
			if prog, ok := programName(x.Args[1], consts); ok && !IsAllowed(prog) {
				out = append(out, fmt.Sprintf("%s:%d: runs %q (not in internal/exec/allowlist.go)", rel, fset.Position(x.Pos()).Line, prog))
			}
		}
		return true
	})
	return out
}
