package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"exe/internal/config"
)

// uiUntranslated is desk text that stays as it is in every language: names
// of products, systems and commands, and lines made of them.
var uiUntranslated = map[string]bool{
	"Mac OS 9": true, "Mac OS X": true, "Debian 13": true, "Alpine 3.24": true, "exe": true,
	"github.com/livid/exe": true, "ollama": true, "openai (ChatGPT)": true, "exe token": true,
	"Cloudflare": true, "my-vm": true, "Workspace": true, "Claude Code": true, "Codex": true,
	"Tailscale": true, "System :menu.txt": true, "Shift+Insert": true, "chatgpt — {0}{1}{2}": true,
	"ollama {0} — {1}{2} — {3}": true, "Jupiter": true, "Firecracker": true, "Ollama": true,
	"OpenAI": true, "GitHub": true, "Hub": true, "Tailscale SSH": true,
}

func uiCatalog(t *testing.T, lang string) map[string]string {
	t.Helper()
	b, err := uiFS.ReadFile("ui/i18n/" + lang + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var cat map[string]string
	if err := json.Unmarshal(b, &cat); err != nil {
		t.Fatalf("%s.json: %v", lang, err)
	}
	return cat
}

// uiTextKeys lists what the desk asks its catalog for: the key of every
// L("…") and L`…` in its script (a template's ${…} numbered {0}, {1}, …),
// and the text and tips of its markup, which l10nDom puts into the language.
func uiTextKeys(t *testing.T) []string {
	t.Helper()
	page := string(uiHTML)
	start := strings.Index(page, "<script>\n\"use strict\";")
	end := strings.Index(page[start:], "</script>") + start
	var keys []string
	// markup
	doc, err := html.Parse(strings.NewReader(page[:start] + "</body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	// (an svg's own text is drawing, but its title is a tip, which l10nDom
	// translates too)
	var walk func(n *html.Node, skip bool)
	walk = func(n *html.Node, skip bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "svg", "script", "style", "pre":
				skip = true
			case "title":
				if n.Parent != nil && n.Parent.Data == "svg" {
					skip = false
				}
			}
			for _, a := range n.Attr {
				if !skip && (a.Key == "title" || a.Key == "placeholder" || a.Key == "aria-label") && hasWord(a.Val) {
					keys = append(keys, a.Val)
				}
			}
		}
		if n.Type == html.TextNode && !skip {
			if s := strings.TrimSpace(n.Data); hasWord(s) {
				keys = append(keys, s)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, skip)
		}
	}
	walk(doc, false)
	// script
	code := page[start:end]
	for i := 0; i+1 < len(code); i++ {
		if code[i] != 'L' || (i > 0 && isIdent(code[i-1])) {
			continue
		}
		switch code[i+1] {
		case '(':
			j := i + 2
			if j < len(code) && code[j] == '"' {
				k := j + 1
				for k < len(code) && code[k] != '"' {
					if code[k] == '\\' {
						k++
					}
					k++
				}
				if k+1 < len(code) && code[k+1] == ')' {
					s, err := strconv.Unquote(code[j : k+1])
					if err != nil {
						t.Fatalf("L(%s): %v", code[j:k+1], err)
					}
					keys = append(keys, s)
				}
			}
		case '`':
			key, _ := templateKey(t, code, i+2)
			keys = append(keys, key) // and on into it: its ${…} may hold an L of its own
		}
	}
	// Configuration's tabs, labels and hints go through L where they are drawn
	fields := code[strings.Index(code, "const CONFIG_FIELDS = ["):]
	fields = fields[:strings.Index(fields, "\n];")]
	for _, m := range regexp.MustCompile(`(?:group|label|hint): ("(?:[^"\\]|\\.)*")`).FindAllStringSubmatch(fields, -1) {
		s, err := strconv.Unquote(m[1])
		if err != nil {
			t.Fatalf("CONFIG_FIELDS %s: %v", m[1], err)
		}
		keys = append(keys, s)
	}
	return keys
}

func hasWord(s string) bool { return regexp.MustCompile(`[A-Za-z]{2}`).MatchString(s) }

func isIdent(c byte) bool {
	return c == '_' || c == '$' || c == '.' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

// templateKey reads the template literal whose text starts at code[i] and
// returns its key and the index of its closing backtick.
func templateKey(t *testing.T, code string, i int) (string, int) {
	var raw strings.Builder
	n := 0
	for i < len(code) {
		switch c := code[i]; {
		case c == '`':
			// the cooked text: JavaScript's escapes are Go's, but for \` and \$
			r := strings.NewReplacer("\\`", "`", `\$`, "$", `"`, `\"`, "\n", `\n`).Replace(raw.String())
			s, err := strconv.Unquote(`"` + r + `"`)
			if err != nil {
				t.Fatalf("template %q: %v", raw.String(), err)
			}
			return s, i
		case c == '\\':
			raw.WriteString(code[i : i+2])
			i += 2
			continue
		case c == '$' && i+1 < len(code) && code[i+1] == '{':
			i = skipExpr(code, i+2)
			raw.WriteString("{" + strconv.Itoa(n) + "}")
			n++
		default:
			raw.WriteByte(c)
		}
		i++
	}
	t.Fatal("unterminated template")
	return "", 0
}

// skipExpr returns the index of the } that closes the ${ expression whose
// text starts at code[i], stepping over strings and templates inside it.
func skipExpr(code string, i int) int {
	depth := 0
	for ; i < len(code); i++ {
		switch c := code[i]; c {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return i
			}
			depth--
		case '"', '\'':
			for i++; i < len(code) && code[i] != c; i++ {
				if code[i] == '\\' {
					i++
				}
			}
		case '`':
			for i++; i < len(code) && code[i] != '`'; i++ {
				if code[i] == '\\' {
					i++
				} else if code[i] == '$' && i+1 < len(code) && code[i+1] == '{' {
					i = skipExpr(code, i+2)
				}
			}
		}
	}
	return i
}

// Every text the desk shows has its line in each catalog, or is a name that
// stays as it is; a line keeps the placeholders of its text, but for a
// plural ending, which a language without plurals leaves out.
func TestUITextTranslated(t *testing.T) {
	seen := map[string]bool{}
	var keys []string
	for _, k := range uiTextKeys(t) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if len(keys) < 800 {
		t.Fatalf("found only %d texts in the desk: is the scan still reading it?", len(keys))
	}
	ph := regexp.MustCompile(`\{\d+\}`)
	for _, lang := range config.Languages[1:] {
		cat := uiCatalog(t, lang)
		for _, k := range keys {
			v, ok := cat[k]
			if !ok {
				if !uiUntranslated[k] {
					t.Errorf("%s: no line for %q", lang, k)
				}
				continue
			}
			want := map[string]bool{}
			for _, p := range ph.FindAllString(k, -1) {
				want[p] = true
			}
			for _, p := range ph.FindAllString(v, -1) {
				if !want[p] {
					t.Errorf("%s: %q puts in %s, which %q does not have", lang, v, p, k)
				}
			}
		}
	}
	zh, ja := uiCatalog(t, "zh-Hans"), uiCatalog(t, "ja")
	for k := range zh {
		if _, ok := ja[k]; !ok {
			t.Errorf("ja: no line for %q, which zh-Hans has", k)
		}
	}
	for k := range ja {
		if _, ok := zh[k]; !ok {
			t.Errorf("zh-Hans: no line for %q, which ja has", k)
		}
	}
}

// The desk page comes in the configured language: the html element names
// it and the page carries its catalog, and each language's page has a tag
// of its own, so a browser's copy of one is never taken for another.
func TestUIPageLanguage(t *testing.T) {
	cfg := &config.Config{}
	s := New(cfg, nil, nil, "", t.TempDir())
	tags := map[string]bool{}
	for _, lang := range config.Languages {
		cfg.Language = lang
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		body := rec.Body.String()
		if !strings.Contains(body, `<html lang="`+lang+`">`) {
			t.Errorf("%s: the page does not name its language", lang)
		}
		i := strings.Index(body, `<script type="application/json" id="l10n">`)
		j := strings.Index(body[i:], "</script>") + i
		var cat map[string]string
		if i < 0 || json.Unmarshal([]byte(body[i+len(`<script type="application/json" id="l10n">`):j]), &cat) != nil {
			t.Fatalf("%s: no catalog in the page", lang)
		}
		if lang == "en" && len(cat) != 0 || lang != "en" && len(cat) < 800 {
			t.Errorf("%s: the page carries %d lines", lang, len(cat))
		}
		tag := rec.Header().Get("ETag")
		if tags[tag] {
			t.Errorf("%s: tag %s is another language's too", lang, tag)
		}
		tags[tag] = true
	}
	// a language the desk does not have draws it in English
	cfg.Language = "fr"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), `<html lang="en">`) {
		t.Errorf("an unknown language did not fall back to English")
	}
}

// Configuration → General saves the language: in its own case whatever the
// request's, a language the desk does not have refused, and a change told
// to every desk on the layout stream (which names the language as well).
func TestConfigLanguage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("EXE_HOME", home)
	if filepath.Dir(config.Path()) != home {
		t.Fatalf("config path %s is not the test's", config.Path())
	}
	s := New(config.Default(), nil, nil, "", home)
	ch := make(chan []byte, 8)
	s.ui.mu.Lock()
	s.ui.subs = map[chan []byte]struct{}{ch: {}}
	s.ui.mu.Unlock()
	put := func(body string) int {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("PUT", "/v1/config", bytes.NewBufferString(body)))
		return rec.Code
	}
	if code := put(`{"language":"fr"}`); code != http.StatusBadRequest {
		t.Errorf("an unknown language: got %d, want 400", code)
	}
	if code := put(`{"language":"JA"}`); code != http.StatusOK {
		t.Fatalf("ja: got %d", code)
	}
	if got := s.Config().Language; got != "ja" {
		t.Errorf("saved %q, want ja", got)
	}
	select {
	case ev := <-ch:
		if string(ev) != `{"lang":"ja"}` {
			t.Errorf("the desks heard %s", ev)
		}
	default:
		t.Errorf("no desk was told of the new language")
	}
	if code := put(`{"language":"ja"}`); code != http.StatusOK {
		t.Fatalf("ja again: got %d", code)
	}
	select {
	case ev := <-ch:
		t.Errorf("the same language was announced again: %s", ev)
	default:
	}
	s.ui.mu.Lock()
	ev := string(s.ui.eventLocked("", s.Config().UILanguage()))
	s.ui.mu.Unlock()
	if !strings.Contains(ev, `"lang":"ja"`) {
		t.Errorf("the layout stream does not name the language: %s", ev)
	}
}
