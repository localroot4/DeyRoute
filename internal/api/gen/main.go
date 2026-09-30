// Command gen generates the Local API transport from internal/api/local.go:
//
//   - internal/api/rpc_gen.go: the unix-socket client (localClient) and the
//     server dispatcher NewLocalHandler;
//   - internal/api/apitest/stub_gen.go: apitest.Stub, an api.Local with one
//     optional function per method for CLI/TUI tests.
//
// It runs through "go generate ./internal/api" (see internal/api/generate.go)
// with the package directory as working directory.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	in := flag.String("in", "local.go", "source file declaring the Local interface")
	rpcOut := flag.String("rpc", "rpc_gen.go", "output file for the client and dispatcher")
	stubOut := flag.String("stub", "apitest/stub_gen.go", "output file for apitest.Stub")
	flag.Parse()
	if err := run(*in, *rpcOut, *stubOut); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(in, rpcOut, stubOut string) error {
	src, err := os.ReadFile(in) // #nosec G304 -- developer tool, path from go:generate
	if err != nil {
		return err
	}
	rpc, stub, err := Generate(src)
	if err != nil {
		return err
	}
	if err := writeSource(rpcOut, rpc); err != nil {
		return err
	}
	return writeSource(stubOut, stub)
}

// writeSource writes a generated Go file with the repository's usual mode.
func writeSource(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644) //nolint:gosec // G306: a source file in the repository, not a secret
}
