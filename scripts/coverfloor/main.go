// Command coverfloor fails when any package in a Go cover profile is below
// a minimum statement coverage. Used by `make cover` (section 15: >= 70%).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

func main() {
	profile := flag.String("profile", "cover.out", "cover profile")
	minPct := flag.Float64("min", 70, "minimum percent per package")
	flag.Parse()
	f, err := os.Open(*profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer func() { _ = f.Close() }()
	total := map[string]int{}
	covered := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		// file.go:1.2,3.4 stmts count
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		pkg := path.Dir(strings.SplitN(fields[0], ":", 2)[0])
		n, _ := strconv.Atoi(fields[1])
		c, _ := strconv.Atoi(fields[2])
		total[pkg] += n
		if c > 0 {
			covered[pkg] += n
		}
	}
	pkgs := make([]string, 0, len(total))
	for p := range total {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	fail := false
	for _, p := range pkgs {
		pct := 100 * float64(covered[p]) / float64(max(total[p], 1))
		mark := "ok"
		if pct < *minPct {
			mark = "BELOW"
			fail = true
		}
		fmt.Printf("%-70s %6.1f%% %s\n", p, pct, mark)
	}
	if fail {
		os.Exit(1)
	}
}
