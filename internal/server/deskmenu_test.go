package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"exe/internal/config"
)

func TestDeskMenuDefaultParses(t *testing.T) {
	menu, err := parseDeskMenu(deskMenuDefault)
	if err != nil {
		t.Fatalf("factory menu rejected: %v", err)
	}
	if len(menu) == 0 || menu[0].Label != "New VM…" || menu[0].Action != "newvm" {
		t.Fatalf("first item wrong: %+v", menu)
	}
	// every documented action in the header is a real one
	seen := map[string]bool{}
	for _, l := range strings.Split(deskMenuDefault, "\n") {
		if !strings.HasPrefix(l, "#   ") {
			continue
		}
		for _, f := range strings.Fields(l[1:]) {
			if _, ok := deskMenuActions[f]; ok {
				seen[f] = true
			}
		}
	}
	for a := range deskMenuActions {
		if !seen[a] {
			t.Errorf("action %q missing from the factory file's header", a)
		}
	}
	var hasCustomize func([]deskMenuItem) bool
	hasCustomize = func(items []deskMenuItem) bool {
		for _, it := range items {
			if it.Action == "customize" || hasCustomize(it.Items) {
				return true
			}
		}
		return false
	}
	if !hasCustomize(menu) {
		t.Fatal("factory menu has no Customize… item")
	}
}

func TestDeskMenuParseShapes(t *testing.T) {
	text := "# c\n\nOpen\tvm demo term\nTools\n\tChat  chat\n\tSub\n\t\tDeep   about\n-\nApps  @apps\n@vms\n"
	menu, err := parseDeskMenu(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(menu) != 5 {
		t.Fatalf("want 5 root items, got %d: %+v", len(menu), menu)
	}
	if menu[0].Action != "vm" || strings.Join(menu[0].Args, ",") != "demo,term" {
		t.Fatalf("vm item wrong: %+v", menu[0])
	}
	if menu[1].Label != "Tools" || len(menu[1].Items) != 2 || menu[1].Items[1].Items[0].Action != "about" {
		t.Fatalf("nested submenu wrong: %+v", menu[1])
	}
	if !menu[2].Sep || menu[3].Gen != "apps" || menu[3].Label != "Apps" || menu[4].Gen != "vms" || menu[4].Label != "" {
		t.Fatalf("separator/generator items wrong: %+v", menu[2:])
	}
}

func TestDeskMenuParseErrors(t *testing.T) {
	cases := []struct{ text, want string }{
		{"Foo  bogus\n", "line 1: unknown action \"bogus\""},
		{"New VM newvm\n", "line 1: \"New VM newvm\" has no action"},
		{"Windows\n", "line 1: \"Windows\" has no action"},
		{"A  newvm\n  B  about\n", "line 2: \"A\" already has an action"},
		{"  A  newvm\n", "line 1: indented, but"},
		{"T\n    A  about\n  B  about\n", "line 3: indentation doesn't line up"},
		{"A  newvm extra\n", "line 1: newvm takes no argument"},
		{"A  vm\n", "line 1: vm needs <name>"},
		{"A  vm demo nope\n", "line 1: \"nope\" is not a VM tab"},
		{"A  url ftp://x\n", "line 1: url needs an http://"},
		{"A  edit ../etc/passwd\n", "line 1: \"../etc/passwd\" is not a Workspace path"},
		{"A  @nope\n", "line 1: unknown list @nope"},
		{"@vms extra\n", "line 1: @vms takes nothing after it"},
		{"A\n B\n  C\n   D\n    E  about\n", "line 5: submenus nest too deep"},
		{strings.Repeat("x", 61) + "  about\n", "line 1: label is too long"},
	}
	for _, c := range cases {
		_, err := parseDeskMenu(c.text)
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%q: got %v, want prefix %q", c.text, err, c.want)
		}
	}
}

func TestDeskMenuHandlers(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ui/menu", s.handleDeskMenuGet)
	mux.HandleFunc("PUT /v1/ui/menu", s.handleDeskMenuPut)
	do := func(method, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, "/v1/ui/menu", strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var res map[string]any
		json.Unmarshal(rec.Body.Bytes(), &res)
		return rec.Code, res
	}

	code, res := do("GET", "")
	if code != 200 || res["default"] != true || res["text"] != deskMenuDefault || res["error"] != nil {
		t.Fatalf("fresh GET: %d %v", code, res)
	}

	// a bad file is refused with its line and nothing is written
	code, res = do("PUT", "Foo  nothing\n")
	if code != 400 || !strings.HasPrefix(res["error"].(string), "line 1: unknown action") {
		t.Fatalf("bad PUT: %d %v", code, res)
	}
	if _, err := os.Stat(s.deskMenuPath()); !os.IsNotExist(err) {
		t.Fatal("bad PUT left a file behind")
	}

	custom := "Hello  about\n-\nCustomize…  customize\n"
	code, res = do("PUT", custom)
	if code != 200 || res["default"] != false || res["text"] != custom || res["modified"] == nil {
		t.Fatalf("good PUT: %d %v", code, res)
	}
	if b, _ := os.ReadFile(s.deskMenuPath()); string(b) != custom {
		t.Fatalf("stored %q", b)
	}
	code, res = do("GET", "")
	menu := res["menu"].([]any)
	if code != 200 || res["default"] != false || len(menu) != 3 {
		t.Fatalf("GET after PUT: %d %v", code, res)
	}

	// a stored file that no longer parses reports the problem but still
	// hands out a working (factory) tree
	os.WriteFile(s.deskMenuPath(), []byte("Broken  nope\n"), 0o644)
	code, res = do("GET", "")
	if code != 200 || res["error"] == nil || res["text"] != "Broken  nope\n" || len(res["menu"].([]any)) < 5 {
		t.Fatalf("GET with broken file: %d %v", code, res)
	}

	// blank text restores the factory menu and removes the file
	code, res = do("PUT", "  \n\n")
	if code != 200 || res["default"] != true || res["text"] != deskMenuDefault {
		t.Fatalf("reset PUT: %d %v", code, res)
	}
	if _, err := os.Stat(s.deskMenuPath()); !os.IsNotExist(err) {
		t.Fatal("reset left the file in place")
	}
}
