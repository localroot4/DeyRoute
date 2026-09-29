package errors

import (
	stderrors "errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/ERRORS.md")

func TestCatalogComplete(t *testing.T) {
	for code, in := range catalog {
		if !ValidCode(code) {
			t.Errorf("%s: invalid code shape", code)
		}
		if in.Code != code {
			t.Errorf("%s: entry has mismatched Code %s", code, in.Code)
		}
		if strings.TrimSpace(in.Message) == "" || strings.TrimSpace(in.Why) == "" || strings.TrimSpace(in.Fix) == "" {
			t.Errorf("%s: Message, Why and Fix are all mandatory", code)
		}
	}
}

// Every Code constant declared in codes.go must have a catalog entry.
func TestEveryConstantInCatalog(t *testing.T) {
	src, err := os.ReadFile("codes.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`Code = "(DEY-[A-Z][0-9]{3})"`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		if _, ok := catalog[Code(m[1])]; !ok {
			t.Errorf("%s declared but missing from catalog", m[1])
		}
	}
}

func TestSpecCodesPresent(t *testing.T) {
	// Codes named explicitly in the specification (section 13).
	for _, c := range []Code{I001, I002, I003, I004, I005, I006, C001, C002, C003, C004, C005, C006,
		N001, N002, N003, N004, N005, P010, P012, P013, P014, P015, T001, T002, T003, T004,
		B001, B002, B003, B004, B040, F001, F002, F003, S001, S002, S003, X000, X001, X002, X003} {
		if _, ok := Lookup(c); !ok {
			t.Errorf("spec code %s missing", c)
		}
	}
}

func TestFormat(t *testing.T) {
	e := New(P012, Params{"port": "443/tcp", "process": "nginx (pid 1234)", "addr": "0.0.0.0:443"}).
		WithFix("choose another port, or stop nginx: systemctl stop nginx").
		WithLog("/var/log/deyroute/hub.log")
	want := "✖ DEY-P012  Port 443/tcp is already in use\n" +
		"  Why:  nginx (pid 1234) is listening on 0.0.0.0:443\n" +
		"  Fix:  choose another port, or stop nginx: systemctl stop nginx\n" +
		"  Log:  /var/log/deyroute/hub.log (search DEY-P012)\n"
	if got := e.Format(true); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.HasPrefix(e.Format(false), "x DEY-P012") {
		t.Fatalf("ascii format: %q", e.Format(false))
	}
}

func TestMissingPlaceholder(t *testing.T) {
	e := New(P012, nil)
	if !strings.Contains(e.Message(), "?") {
		t.Fatalf("missing placeholder should render ?: %q", e.Message())
	}
}

func TestWrapIsAs(t *testing.T) {
	cause := fmt.Errorf("boom")
	e := Wrap(C014, cause, Params{"path": "/x"})
	outer := fmt.Errorf("ctx: %w", e)
	if !stderrors.Is(outer, cause) {
		t.Fatal("cause not reachable")
	}
	if !stderrors.Is(outer, New(C014, nil)) {
		t.Fatal("Is by code failed")
	}
	if got := As(outer); got.Code != C014 {
		t.Fatalf("As: %s", got.Code)
	}
	if got := As(cause); got.Code != X000 {
		t.Fatalf("plain error should map to X000, got %s", got.Code)
	}
	if !HasCode(Wrap(B003, New(P012, nil), nil), P012) {
		t.Fatal("HasCode should see nested code")
	}
	if As(nil) != nil {
		t.Fatal("As(nil) must be nil")
	}
}

func TestExitCodes(t *testing.T) {
	if New(X003, nil).ExitCode() != ExitSystem {
		t.Fatal("X must be system exit")
	}
	for _, c := range []Code{C001, P012, T001, N001} {
		if ExitCodeFor(c) != ExitUser {
			t.Fatalf("%s must be user exit", c)
		}
	}
}

func TestErrorsDocUpToDate(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "docs", "ERRORS.md")
	want := MarkdownDoc()
	if *update {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run make docs)", path, err)
	}
	if string(got) != want {
		t.Fatal("docs/ERRORS.md is stale; run: make docs")
	}
}
