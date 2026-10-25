package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"exe/internal/config"
	"exe/internal/dictlabels"
)

// The test binary is its own stand-in for the Codex CLI: run with
// EXE_DICT_FAKE set to a folder, it answers as `codex app-server` would
// (fakeCodexAppServer) instead of running the tests.
func TestMain(m *testing.M) {
	if dir := os.Getenv("EXE_DICT_FAKE"); dir != "" {
		fakeCodexAppServer(dir)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeDictCodex points the dictionary at the stand-in: a script that runs
// this test binary as a Codex app server working in dir, whose answer is
// answer ("" for a session that fails).
func fakeDictCodex(t *testing.T, dir, answer string) {
	t.Helper()
	if answer != "" {
		if err := os.WriteFile(filepath.Join(dir, "answer.json"), []byte(answer), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nEXE_DICT_FAKE=" + shQuote(dir) + " exec " + shQuote(self) + " \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	was := dictCodexPath
	dictCodexPath = func() string { return bin }
	t.Cleanup(func() { dictCodexPath = was })
}

// fakeCodexAppServer speaks just enough of the app server's JSON-RPC: it
// notes its arguments and what thread/start and turn/start asked for in
// dir, a line per run in runs and per turn in turns ("spell" or "entry"),
// then plays each turn. A spelling pass (its schema asks for a verdict)
// answers spell.json, a word by default. An entry turn plays a reasoning
// pass with a summary, the answer in fragments and the tokens — or, with
// no answer.json, fails. For trying the app by hand on a scratch daemon,
// EXE_DICT_FAKE_PACE (a duration) spaces an entry turn's notes out, and
// "{{word}}" in answer.json becomes the word looked up. A turn waits, once
// begun, while a gate file stands in dir: hold-spell holds the spelling
// verdict, hold-entry the entry's reasoning — a test that needs two
// lookups in flight at once makes the gate before the first and removes
// it after the second.
func fakeCodexAppServer(dir string) {
	note := func(name, text string) {
		f, _ := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		f.WriteString(text)
		f.Close()
	}
	hold := func(gate string) {
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(filepath.Join(dir, gate)); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	note("args", strings.Join(os.Args[1:], " ")+"\n")
	note("runs", "run\n")
	time.Sleep(300 * time.Millisecond) // long enough for a second lookup to join
	enc := json.NewEncoder(os.Stdout)
	pace, _ := time.ParseDuration(os.Getenv("EXE_DICT_FAKE_PACE"))
	say := func(method string, params any) { enc.Encode(map[string]any{"method": method, "params": params}) }
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var m struct {
			ID     json.RawMessage
			Method string
			Params json.RawMessage
		}
		json.Unmarshal(sc.Bytes(), &m)
		switch m.Method {
		case "initialize":
			enc.Encode(map[string]any{"id": m.ID, "result": map[string]any{"userAgent": "fake"}})
		case "thread/start":
			os.WriteFile(filepath.Join(dir, "thread"), m.Params, 0o600)
			enc.Encode(map[string]any{"id": m.ID, "result": map[string]any{"thread": map[string]any{"id": "t1"}}})
		case "turn/start":
			enc.Encode(map[string]any{"id": m.ID, "result": map[string]any{"turn": map[string]any{"id": "u1"}}})
			if strings.Contains(string(m.Params), `"verdict"`) {
				note("turns", "spell\n")
				note("spellturn", string(m.Params))
				hold("hold-spell")
				v, err := os.ReadFile(filepath.Join(dir, "spell.json"))
				if err != nil {
					v = []byte(`{"verdict":"word","word":"","suggestions":[]}`)
				}
				say("item/started", map[string]any{"item": map[string]any{"type": "agentMessage", "id": "s1", "text": ""}})
				say("item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "id": "s1", "text": string(v)}})
				say("turn/completed", map[string]any{"turn": map[string]any{"status": "completed"}})
				continue
			}
			note("turns", "entry\n")
			os.WriteFile(filepath.Join(dir, "turn"), m.Params, 0o600)
			hold("hold-entry")
			answer, err := os.ReadFile(filepath.Join(dir, "answer.json"))
			if err != nil {
				say("error", map[string]any{"error": map[string]any{"message": "boom"}, "willRetry": false})
				say("turn/completed", map[string]any{"turn": map[string]any{"status": "failed", "error": map[string]any{"message": "boom"}}})
				continue
			}
			var turn struct{ Input []struct{ Text string } }
			json.Unmarshal(m.Params, &turn)
			if len(turn.Input) > 0 {
				if _, rest, ok := strings.Cut(turn.Input[0].Text, "The reader looked up: "); ok {
					w, _, _ := strings.Cut(rest, "\n")
					w, _ = strconv.Unquote(w)
					answer = []byte(strings.ReplaceAll(string(answer), "{{word}}", w))
				}
			}
			say("item/started", map[string]any{"item": map[string]any{"type": "reasoning", "id": "r1"}})
			time.Sleep(pace)
			say("item/reasoning/summaryTextDelta", map[string]any{"itemId": "r1", "summaryIndex": 0, "delta": "**Checking the senses**"})
			// Sol may follow the heading with a detailed paragraph. It must
			// never reach the progress stream, even one fragment at a time.
			for _, delta := range []string{"\n\n", "A detailed explanation", " of the dictionary research."} {
				say("item/reasoning/summaryTextDelta", map[string]any{"itemId": "r1", "summaryIndex": 0, "delta": delta})
			}
			time.Sleep(pace)
			say("item/completed", map[string]any{"item": map[string]any{"type": "reasoning", "id": "r1"}})
			time.Sleep(pace)
			say("item/started", map[string]any{"item": map[string]any{"type": "agentMessage", "id": "m1", "text": ""}})
			for r := []rune(string(answer)); len(r) > 0; {
				n := min(len(r), 10)
				say("item/agentMessage/delta", map[string]any{"itemId": "m1", "delta": string(r[:n])})
				r = r[n:]
				time.Sleep(pace / 20)
			}
			say("item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "id": "m1", "text": string(answer)}})
			say("thread/tokenUsage/updated", map[string]any{"tokenUsage": map[string]any{"total": map[string]any{
				"totalTokens": 100, "inputTokens": 80, "cachedInputTokens": 0, "outputTokens": 20, "reasoningOutputTokens": 5}}})
			say("turn/completed", map[string]any{"turn": map[string]any{"status": "completed"}})
		}
	}
}

func TestDictSummaryHeading(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"empty", " \n ", ""},
		{"opening marker", "**", ""},
		{"split opening marker", "*", ""},
		{"partial heading", "**Checking the", "Checking the"},
		{"split closing marker", "**Checking the senses*", "Checking the senses"},
		{"heading", "**Checking the senses**", "Checking the senses"},
		{"paragraph", "**Checking the senses**\n\nA detailed explanation.", "Checking the senses"},
		{"inline paragraph", "**Checking the senses** A detailed explanation.", "Checking the senses"},
		{"plain", "Checking the senses\n\nA detailed explanation.", "Checking the senses"},
		{"CRLF", "Checking the senses\r\nA detailed explanation.", "Checking the senses"},
		{"unicode line", "Checking the senses\u2028A detailed explanation.", "Checking the senses"},
		{"spacing", "  **Checking\tthe senses**  ", "Checking the senses"},
		{"unbounded line", strings.Repeat("a", 120), strings.Repeat("a", 95) + "…"},
		{"unicode limit", "**" + strings.Repeat("字", 120) + "**", strings.Repeat("字", 95) + "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dictSummaryHeading(tc.text); got != tc.want {
				t.Errorf("heading = %q, want %q", got, tc.want)
			}
		})
	}
}

func dictLines(t *testing.T, resp *http.Response) []map[string]any {
	t.Helper()
	var lines []map[string]any
	dec := json.NewDecoder(resp.Body)
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, m)
	}
	return lines
}

func dictPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url+"/v1/dict", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

const dictAnswer = `{"found":true,"headword":"serendipity","note":"","forms":"",
"pronunciations":[{"label":"英","ipa":"/ˌserənˈdɪpəti/"},{"label":"美","ipa":"/ˌsɛrənˈdɪpəti/"}],
"senses":[{"pos":"n.","gloss":"意外收获；机缘巧合","definition":"偶然发现美好事物的机缘。"}],
"examples":[{"text":"It was pure serendipity.","translation":"这纯属机缘巧合。"},
 {"text":"Although she was looking for a date, serendipity led her to a letter.","translation":"她原本在找一个日期，却偶然发现了一封信。"}],
"etymology":"沃波尔于1754年创造。","related":[{"word":"serendipitous","relation":"派生词","gloss":"机缘巧合的"}],
"literary":{"note":"书面色彩。","quotes":[]},"suggestions":[]}`

// A word the dictionary does not keep is written by one ephemeral Codex
// session on Sol 6.1 at xhigh, kept, and answered from the database after.
func TestDictWritesThenKeeps(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	if r, _ := http.Get(ts.URL + "/v1/dict?from=en&to=zh&q=serendipity"); r.StatusCode != http.StatusNotFound {
		t.Fatalf("GET before = %d, want 404", r.StatusCode)
	}

	resp := dictPost(t, ts.URL, `{"from":"en","to":"zh","q":"  Serendipity "}`)
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type = %q", ct)
	}
	lines := dictLines(t, resp)
	if len(lines) < 3 || lines[0]["writing"] != true || lines[0]["model"] != "gpt-6.1-sol" || lines[0]["effort"] != "xhigh" {
		t.Fatalf("lines = %v", lines)
	}
	// on the way: the session's steps, and the entry a fragment at a time
	var kinds []string
	var text strings.Builder
	summary := ""
	for _, l := range lines[1 : len(lines)-1] {
		if st, ok := l["step"].(map[string]any); ok {
			kinds = append(kinds, st["kind"].(string))
			if st["kind"] == "summary" {
				summary, _ = st["text"].(string)
			}
		} else if d, ok := l["text"].(string); ok {
			text.WriteString(d)
		}
	}
	if got := strings.Join(kinds, " "); got != "session spell spelled think summary thought answer usage" {
		t.Errorf("steps = %q", got)
	}
	if summary != "Checking the senses" {
		t.Errorf("summary = %q", summary)
	}
	if text.String() != dictAnswer {
		t.Errorf("streamed text = %q", text.String())
	}
	got, _ := lines[len(lines)-1]["entry"].(map[string]any)
	if got == nil || got["q"] != "serendipity" || got["written"] != true || got["model"] != "gpt-6.1-sol" {
		t.Fatalf("entry line = %v", lines[len(lines)-1])
	}
	if e, _ := got["entry"].(map[string]any); e == nil || e["headword"] != "serendipity" {
		t.Fatalf("entry = %v", got["entry"])
	}
	if tok, _ := got["tokens"].(map[string]any); tok == nil || tok["totalTokens"] != 100.0 {
		t.Errorf("tokens = %v", got["tokens"])
	}

	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	for _, want := range []string{"app-server", "--disable shell_tool", "--disable unified_exec"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("codex args %q lack %q", args, want)
		}
	}
	var thread, turn struct {
		Ephemeral           bool
		Model, Sandbox, Cwd string
		ApprovalPolicy      string
		Effort, Summary     string
		OutputSchema        map[string]any
		Input               []struct{ Text string }
	}
	b, _ := os.ReadFile(filepath.Join(dir, "thread"))
	json.Unmarshal(b, &thread)
	if !thread.Ephemeral || thread.Model != "gpt-6.1-sol" || thread.Sandbox != "read-only" || thread.ApprovalPolicy != "never" {
		t.Errorf("thread/start = %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "turn"))
	json.Unmarshal(b, &turn)
	if turn.Effort != "xhigh" || turn.Summary != "detailed" || turn.OutputSchema["type"] != "object" || len(turn.Input) != 1 {
		t.Fatalf("turn/start = %s", b)
	}
	posSchema := turn.OutputSchema["properties"].(map[string]any)["senses"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["pos"].(map[string]any)
	if posSchema["pattern"] != dictlabels.Pattern {
		t.Fatalf("English label constraint not sent to Codex: %v", posSchema)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "spellturn"))
	if !strings.Contains(string(b), `"effort":"medium"`) || !strings.Contains(string(b), `They typed: \"serendipity\"`) {
		t.Errorf("spelling turn = %s", b)
	}
	prompt := turn.Input[0].Text
	for _, want := range []string{"English–Chinese", `"serendipity"`, "Simplified Chinese", `"英" and "美"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}

	// kept: a GET answers it, and a second lookup starts no session
	r, err := http.Get(ts.URL + "/v1/dict?from=en&to=zh&q=SERENDIPITY")
	if err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("GET after = %v %v", r.StatusCode, err)
	}
	r.Body.Close()
	lines = dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"zh","q":"serendipity"}`))
	if len(lines) != 1 || lines[0]["entry"] == nil {
		t.Fatalf("kept lines = %v", lines)
	}
	if e := lines[0]["entry"].(map[string]any); e["written"] != nil {
		t.Fatalf("a kept entry says written: %v", e)
	}
	if runs, _ := os.ReadFile(filepath.Join(dir, "runs")); strings.Count(string(runs), "run") != 1 {
		t.Fatalf("codex ran %d times", strings.Count(string(runs), "run"))
	}
	// another pair of languages is another entry
	if r, _ := http.Get(ts.URL + "/v1/dict?from=en&to=ja&q=serendipity"); r.StatusCode != http.StatusNotFound {
		t.Fatalf("en→ja = %d, want 404", r.StatusCode)
	}
}

// dictTurns counts the turns the stand-in played, by kind.
func dictTurns(dir, kind string) int {
	b, _ := os.ReadFile(filepath.Join(dir, "turns"))
	return strings.Count(string(b), kind+"\n")
}

// An obvious typo gets no page of its own: the spelling pass reads it as
// the word it stands for, which is what is written and kept; the typo is
// remembered, so looking it up again reads that word's entry at once.
func TestDictFixesTypo(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	os.WriteFile(filepath.Join(dir, "spell.json"), []byte(`{"verdict":"typo","word":"serendipity","suggestions":[]}`), 0o600)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	lines := dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"zh","q":"serendipty"}`))
	var spelled map[string]any
	for _, l := range lines {
		if st, ok := l["step"].(map[string]any); ok && st["kind"] == "spelled" {
			spelled = st
		}
	}
	if spelled["verdict"] != "typo" || spelled["word"] != "serendipity" {
		t.Fatalf("spelled = %v", spelled)
	}
	got, _ := lines[len(lines)-1]["entry"].(map[string]any)
	if got == nil || got["q"] != "serendipity" || got["corrected"] != "serendipty" {
		t.Fatalf("last line = %v", lines[len(lines)-1])
	}
	turn, _ := os.ReadFile(filepath.Join(dir, "turn"))
	if !strings.Contains(string(turn), `\"serendipity\"`) || strings.Contains(string(turn), "serendipty") {
		t.Fatalf("the entry was asked for the typo: %s", turn)
	}

	// the typo has no page; it reads the word's
	r, _ := http.Get(ts.URL + "/v1/dict?from=en&to=zh&q=serendipty&exact=1")
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("the typo was kept as a page: %d", r.StatusCode)
	}
	r, _ = http.Get(ts.URL + "/v1/dict?from=en&to=zh&q=serendipty")
	var k map[string]any
	json.NewDecoder(r.Body).Decode(&k)
	r.Body.Close()
	if k["q"] != "serendipity" || k["corrected"] != "serendipty" {
		t.Fatalf("GET of the typo = %v", k)
	}
	// again: no session at all, and in another language pair the known
	// typo goes straight to its word, with no second spelling pass
	lines = dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"zh","q":"serendipty"}`))
	if len(lines) != 1 || lines[0]["entry"].(map[string]any)["corrected"] != "serendipty" {
		t.Fatalf("second lookup = %v", lines)
	}
	lines = dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"ja","q":"serendipty"}`))
	if last := lines[len(lines)-1]["entry"].(map[string]any); last["q"] != "serendipity" || last["corrected"] != "serendipty" {
		t.Fatalf("en→ja = %v", lines[len(lines)-1])
	}
	if n, m := dictTurns(dir, "spell"), dictTurns(dir, "entry"); n != 1 || m != 2 {
		t.Fatalf("spelling passes %d, entries written %d; want 1 and 2", n, m)
	}
}

// A typo of a word the dictionary keeps costs the spelling pass alone; a
// lookup that is no word stops after it, with its suggestions; and Exact
// looks the text up as typed, with no pass.
func TestDictSpellingShortcuts(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"zh","q":"serendipity"}`))

	os.WriteFile(filepath.Join(dir, "spell.json"), []byte(`{"verdict":"typo","word":"Serendipity","suggestions":[]}`), 0o600)
	lines := dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"zh","q":"serendipitty"}`))
	if last := lines[len(lines)-1]["entry"].(map[string]any); last["q"] != "serendipity" || last["corrected"] != "serendipitty" {
		t.Fatalf("typo of a kept word = %v", lines[len(lines)-1])
	}

	os.WriteFile(filepath.Join(dir, "spell.json"), []byte(`{"verdict":"unknown","word":"","suggestions":["quartz"]}`), 0o600)
	lines = dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"zh","q":"xqzt"}`))
	if last := lines[len(lines)-1]; last["notfound"] != true || last["suggestions"].([]any)[0] != "quartz" {
		t.Fatalf("no word = %v", last)
	}

	lines = dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"zh","q":"teh","exact":true}`))
	if last := lines[len(lines)-1]["entry"].(map[string]any); last["q"] != "teh" || last["corrected"] != nil {
		t.Fatalf("exact = %v", lines[len(lines)-1])
	}
	if n, m := dictTurns(dir, "spell"), dictTurns(dir, "entry"); n != 3 || m != 2 {
		t.Fatalf("spelling passes %d, entries written %d; want 3 and 2", n, m)
	}
}

// A lookup started in the background: its lines come on done.
type dictAsync struct {
	t    *testing.T
	done chan []map[string]any
}

func dictStart(t *testing.T, url, body string) *dictAsync {
	a := &dictAsync{t: t, done: make(chan []map[string]any, 1)}
	go func() {
		resp, err := http.Post(url+"/v1/dict", "application/json", strings.NewReader(body))
		if err != nil {
			t.Error(err)
			a.done <- nil
			return
		}
		defer resp.Body.Close()
		var lines []map[string]any
		dec := json.NewDecoder(resp.Body)
		for dec.More() {
			var m map[string]any
			if dec.Decode(&m) != nil {
				break
			}
			lines = append(lines, m)
		}
		a.done <- lines
	}()
	return a
}

// entry is the lookup's last line's entry: its q and what it corrected.
func (a *dictAsync) entry() (q, corrected string, lines []map[string]any) {
	a.t.Helper()
	select {
	case lines = <-a.done:
	case <-time.After(20 * time.Second):
		a.t.Fatal("the lookup never finished")
	}
	if len(lines) == 0 {
		a.t.Fatal("no lines")
	}
	e, _ := lines[len(lines)-1]["entry"].(map[string]any)
	if e == nil {
		a.t.Fatalf("last line = %v", lines[len(lines)-1])
	}
	q, _ = e["q"].(string)
	corrected, _ = e["corrected"].(string)
	return q, corrected, lines
}

// waitTurns waits until the stand-in has begun n turns of a kind.
func waitTurns(t *testing.T, dir, kind string, n int) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if dictTurns(dir, kind) >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%d %s turns never began", n, kind)
}

// An exact lookup of a text never joins a session still spelling it: the
// plain lookup of "recieve" is corrected, the exact one, made while the
// spelling pass was out, gets "recieve" as typed from a session of its own.
func TestDictExactNeverJoinsSpelling(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	os.WriteFile(filepath.Join(dir, "spell.json"), []byte(`{"verdict":"typo","word":"receive","suggestions":[]}`), 0o600)
	os.WriteFile(filepath.Join(dir, "hold-spell"), nil, 0o600)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	plain := dictStart(t, ts.URL, `{"from":"en","to":"zh","q":"recieve"}`)
	waitTurns(t, dir, "spell", 1)
	exact := dictStart(t, ts.URL, `{"from":"en","to":"zh","q":"recieve","exact":true}`)
	waitTurns(t, dir, "entry", 1) // the exact session is writing already
	os.Remove(filepath.Join(dir, "hold-spell"))

	if q, c, _ := plain.entry(); q != "receive" || c != "recieve" {
		t.Errorf("plain = %q corrected %q", q, c)
	}
	if q, c, _ := exact.entry(); q != "recieve" || c != "" {
		t.Errorf("exact = %q corrected %q", q, c)
	}
	if n, m := dictTurns(dir, "spell"), dictTurns(dir, "entry"); n != 1 || m != 2 {
		t.Fatalf("spelling passes %d, entries written %d; want 1 and 2", n, m)
	}
}

// Nor does a plain lookup join an exact session for its text: it spells
// the text itself and is corrected, while the exact one keeps its own.
func TestDictPlainNeverJoinsExact(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	os.WriteFile(filepath.Join(dir, "spell.json"), []byte(`{"verdict":"typo","word":"receive","suggestions":[]}`), 0o600)
	os.WriteFile(filepath.Join(dir, "hold-entry"), nil, 0o600)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	exact := dictStart(t, ts.URL, `{"from":"en","to":"zh","q":"recieve","exact":true}`)
	waitTurns(t, dir, "entry", 1)
	plain := dictStart(t, ts.URL, `{"from":"en","to":"zh","q":"recieve"}`)
	waitTurns(t, dir, "entry", 2) // its own session, on receive
	os.Remove(filepath.Join(dir, "hold-entry"))

	if q, c, _ := exact.entry(); q != "recieve" || c != "" {
		t.Errorf("exact = %q corrected %q", q, c)
	}
	if q, c, _ := plain.entry(); q != "receive" || c != "recieve" {
		t.Errorf("plain = %q corrected %q", q, c)
	}
	if n := dictTurns(dir, "spell"); n != 1 {
		t.Fatalf("spelling passes %d; want 1", n)
	}
}

// Lookups whose word matches still share: once "recieve" is read as
// "receive", an exact lookup of "receive" joins that session, and so does
// a plain one.
func TestDictSharesOnceSpelled(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	os.WriteFile(filepath.Join(dir, "spell.json"), []byte(`{"verdict":"typo","word":"receive","suggestions":[]}`), 0o600)
	os.WriteFile(filepath.Join(dir, "hold-entry"), nil, 0o600)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	typo := dictStart(t, ts.URL, `{"from":"en","to":"zh","q":"recieve"}`)
	waitTurns(t, dir, "entry", 1)
	exact := dictStart(t, ts.URL, `{"from":"en","to":"zh","q":"receive","exact":true}`)
	plain := dictStart(t, ts.URL, `{"from":"en","to":"zh","q":"receive"}`)
	time.Sleep(400 * time.Millisecond) // both have joined, or started a session of their own
	os.Remove(filepath.Join(dir, "hold-entry"))

	if q, c, _ := typo.entry(); q != "receive" || c != "recieve" {
		t.Errorf("typo = %q corrected %q", q, c)
	}
	q, c, lines := exact.entry()
	if q != "receive" || c != "" {
		t.Errorf("exact = %q corrected %q", q, c)
	}
	// the joined session's spelling lines name the text they judged
	for _, l := range lines {
		if st, ok := l["step"].(map[string]any); ok && st["kind"] == "spelled" && st["typed"] != "recieve" {
			t.Errorf("spelled step = %v", st)
		}
	}
	if q, c, _ := plain.entry(); q != "receive" || c != "" {
		t.Errorf("plain = %q corrected %q", q, c)
	}
	if n, m := dictTurns(dir, "spell"), dictTurns(dir, "entry"); n != 1 || m != 1 {
		t.Fatalf("spelling passes %d, entries written %d; want 1 and 1", n, m)
	}
}

// A plain lookup that spells its text while an exact session writes the
// same word does not write it again: it follows that session, and its
// reader sees the session's steps and text after its own spelling lines.
func TestDictFollowsASessionOnTheSameWord(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	os.WriteFile(filepath.Join(dir, "hold-entry"), nil, 0o600)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	exact := dictStart(t, ts.URL, `{"from":"en","to":"zh","q":"receive","exact":true}`)
	waitTurns(t, dir, "entry", 1)
	plain := dictStart(t, ts.URL, `{"from":"en","to":"zh","q":"receive"}`)
	waitTurns(t, dir, "spell", 1)
	time.Sleep(400 * time.Millisecond) // the verdict is in, the follow begun
	os.Remove(filepath.Join(dir, "hold-entry"))

	if q, c, _ := exact.entry(); q != "receive" || c != "" {
		t.Errorf("exact = %q corrected %q", q, c)
	}
	q, c, lines := plain.entry()
	if q != "receive" || c != "" {
		t.Errorf("plain = %q corrected %q", q, c)
	}
	var kinds []string
	text := 0
	for _, l := range lines {
		if st, ok := l["step"].(map[string]any); ok {
			kinds = append(kinds, st["kind"].(string))
		} else if l["text"] != nil {
			text++
		}
	}
	if got := strings.Join(kinds, " "); got != "session spell spelled think summary thought answer usage" || text == 0 {
		t.Errorf("followed steps = %q, %d text lines", got, text)
	}
	if n, m := dictTurns(dir, "spell"), dictTurns(dir, "entry"); n != 1 || m != 1 {
		t.Fatalf("spelling passes %d, entries written %d; want 1 and 1", n, m)
	}
}

// dictStreamReader reads a window's shared stream line by line.
type dictStreamReader struct {
	dec  *json.Decoder
	body interface{ Close() error }
}

func openDictStream(t *testing.T, url string) *dictStreamReader {
	t.Helper()
	resp, err := http.Get(url + "/v1/dict/stream")
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("stream content-type = %q", ct)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &dictStreamReader{dec: json.NewDecoder(resp.Body), body: resp.Body}
}

// until reads lines until one satisfies want, and returns every line read
func (r *dictStreamReader) until(t *testing.T, want func(map[string]any) bool) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for {
		var m map[string]any
		if err := r.dec.Decode(&m); err != nil {
			t.Fatalf("stream ended: %v (after %d lines)", err, len(lines))
		}
		lines = append(lines, m)
		if want(m) {
			return lines
		}
	}
}

func dictStartFlight(t *testing.T, url, body string) map[string]any {
	t.Helper()
	resp, err := http.Post(url+"/v1/dict/start", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start %s: %d %v", body, resp.StatusCode, m)
	}
	return m
}

// A window follows its sessions on one shared stream: a start answers at
// once with the flight, whose lines — steps, the entry in fragments, the
// last line — come on the stream tagged with it. A kept word's start is
// its entry.
func TestDictSharedStream(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close) // after the streams close: Close waits for open requests

	st := openDictStream(t, ts.URL)
	st.until(t, func(m map[string]any) bool { return m["ready"] == true })
	got := dictStartFlight(t, ts.URL, `{"from":"en","to":"zh","q":"Serendipity"}`)
	fid, _ := got["flight"].(string)
	if fid == "" || got["key"] != "serendipity" || got["q"] != "serendipity" || got["model"] != "gpt-6.1-sol" {
		t.Fatalf("start = %v", got)
	}
	lines := st.until(t, func(m map[string]any) bool { return m["flight"] == fid && m["entry"] != nil })
	var text strings.Builder
	announced, steps := false, 0
	for _, l := range lines {
		if l["flight"] != fid {
			continue
		}
		if b, ok := l["began"].(map[string]any); ok && b["q"] == "serendipity" && b["from"] == "en" {
			announced = true
		}
		if l["step"] != nil {
			steps++
		}
		if d, ok := l["text"].(string); ok {
			text.WriteString(d)
		}
	}
	if !announced || steps < 5 || text.String() != dictAnswer {
		t.Fatalf("announced %v, %d steps, text %q", announced, steps, text.String())
	}
	last := lines[len(lines)-1]["entry"].(map[string]any)
	if last["q"] != "serendipity" || last["written"] != true || last["corrected"] != nil {
		t.Fatalf("last line = %v", last)
	}

	if k := dictStartFlight(t, ts.URL, `{"from":"en","to":"zh","q":"serendipity"}`); k["entry"] == nil {
		t.Fatalf("a kept word's start = %v", k)
	}

	// a window that connects later still finds the finished flight, its
	// lines and its end
	late := openDictStream(t, ts.URL)
	lines = late.until(t, func(m map[string]any) bool { return m["ready"] == true })
	if lines[0]["flight"] != fid || lines[0]["began"] == nil || lines[len(lines)-2]["entry"] == nil {
		t.Fatalf("a late window read %d lines: first %v, before ready %v", len(lines), lines[0], lines[len(lines)-2])
	}
}

// No ceiling: every lookup gets its session at once — five held mid-entry
// together, then all written.
func TestDictNoSessionCeiling(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close) // after the streams close: Close waits for open requests

	gate := filepath.Join(dir, "hold-entry")
	os.WriteFile(gate, nil, 0o600)
	words := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	fids := map[string]string{}
	for _, w := range words {
		fids[dictStartFlight(t, ts.URL, `{"from":"en","to":"zh","q":"`+w+`"}`)["flight"].(string)] = w
	}
	deadline := time.Now().Add(10 * time.Second)
	for dictTurns(dir, "entry") < len(words) {
		if time.Now().After(deadline) {
			os.Remove(gate)
			t.Fatalf("only %d of %d sessions reached their entry together", dictTurns(dir, "entry"), len(words))
		}
		time.Sleep(50 * time.Millisecond)
	}
	os.Remove(gate)
	st := openDictStream(t, ts.URL)
	done := map[string]bool{}
	st.until(t, func(m map[string]any) bool {
		if f, ok := m["flight"].(string); ok && m["entry"] != nil {
			done[f] = true
		}
		return len(done) == len(words)
	})
	for f := range fids {
		if !done[f] {
			t.Fatalf("flight %s (%s) never ended", f, fids[f])
		}
	}
}

// Two lookups of one word at once share one session.
func TestDictSharesASession(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(ts.URL+"/v1/dict", "application/json",
				strings.NewReader(`{"from":"la","to":"zh","q":"amor"}`))
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			var last map[string]any
			dec := json.NewDecoder(resp.Body)
			for dec.More() {
				last = nil
				dec.Decode(&last)
			}
			if last["entry"] == nil {
				t.Errorf("last line = %v", last)
			}
		}()
	}
	wg.Wait()
	if runs, _ := os.ReadFile(filepath.Join(dir, "runs")); strings.Count(string(runs), "run") != 1 {
		t.Fatalf("codex ran %d times", strings.Count(string(runs), "run"))
	}
	turn, _ := os.ReadFile(filepath.Join(dir, "turn"))
	if !strings.Contains(string(turn), "Latin–Chinese") || !strings.Contains(string(turn), "macrons") {
		t.Fatalf("turn/start:\n%s", turn)
	}
}

// A lookup that is no word of the source language comes back with
// suggestions, and is not kept.
func TestDictNotAWord(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, `{"found":false,"headword":"","note":"","forms":"","pronunciations":[],"senses":[],
"examples":[],"etymology":"","related":[],"literary":{"note":"","quotes":[]},"suggestions":["serendipity"]}`)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	lines := dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"ko","q":"serendipty"}`))
	last := lines[len(lines)-1]
	if last["notfound"] != true || len(last["suggestions"].([]any)) != 1 {
		t.Fatalf("lines = %v", lines)
	}
	if r, _ := http.Get(ts.URL + "/v1/dict?from=en&to=ko&q=serendipty"); r.StatusCode != http.StatusNotFound {
		t.Fatalf("a non-word was kept: %d", r.StatusCode)
	}
}

// A session that fails says why, and nothing is kept.
func TestDictSessionFails(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, "")
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	lines := dictLines(t, dictPost(t, ts.URL, `{"from":"fr","to":"zh","q":"maison"}`))
	if last := lines[len(lines)-1]; last["error"] != "Codex: boom" {
		t.Fatalf("lines = %v", lines)
	}
	if r, _ := http.Get(ts.URL + "/v1/dict?from=fr&to=zh&q=maison"); r.StatusCode != http.StatusNotFound {
		t.Fatalf("a failed session kept something: %d", r.StatusCode)
	}
}

// Without the Codex CLI a word the dictionary lacks cannot be written; the
// answer says so in the words the app shows.
func TestDictNoBackend(t *testing.T) {
	was := dictCodexPath
	dictCodexPath = func() string { return "" }
	defer func() { dictCodexPath = was }()
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := dictPost(t, ts.URL, `{"from":"de","to":"zh","q":"Haus"}`)
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusServiceUnavailable || body["error"] != "No Usable LLM Backend" {
		t.Fatalf("status %d, body %v", resp.StatusCode, body)
	}
}

func TestDictBadLookups(t *testing.T) {
	was := dictCodexPath
	dictCodexPath = func() string { return "" } // never a real session
	defer func() { dictCodexPath = was }()
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for _, body := range []string{
		`{"from":"xx","to":"zh","q":"word"}`,
		`{"from":"en","to":"fr","q":"word"}`,
		`{"from":"en","to":"zh","q":"   "}`,
		`{"from":"en","to":"zh","q":"` + strings.Repeat("a", 81) + `"}`,
		`{"from":"en","to":"zh","q":"bell\u0007"}`,
	} {
		if r := dictPost(t, ts.URL, body); r.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", body, r.StatusCode)
		}
	}
}
