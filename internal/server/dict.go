// The Dict app's dictionary.
//
// An entry is looked up in the node's own dictionary, ~/.exe/dict.db, and
// written when it is not there: an ephemeral Codex session (threads of
// codex app-server's that are never saved and never join the resume list)
// on gpt-6.1-sol, its answer held to a JSON schema, then kept, so every
// later lookup of the same word in the same pair of languages is answered
// from the database. Before the entry, a quick spelling pass (medium
// effort, a few seconds) reads an obvious typo as the word it stands for:
// that word is what gets written and kept, and the typo is remembered as
// one, never given a page of its own; a lookup that is no word at all ends
// there with suggestions, before the long session (xhigh). A host without
// the Codex CLI can still read what is kept, and says "No Usable LLM
// Backend" for the rest.
//
// GET /v1/dict?from=en&to=zh&q=word answers a kept entry — for a known
// typo, its word's, with "corrected" saying what was typed (exact=1 reads
// the text as typed) — and 404 when there is none. POST /v1/dict with
// {"from","to","q"} answers newline-delimited JSON: a kept entry at once,
// as one {"entry":…} line. Otherwise the session is watched as it works:
// {"writing":true,"model","effort","wait"}, then {"step":{"t","kind",…}}
// lines — t the seconds since the session began; kind "session" (it
// started), "spell" and "spelled" (the spelling pass began, and its
// "verdict" on the "typed" text: "word", "typo" with the "word" it stands
// for — its key, and "shown", as the pass spelled it — or "unknown"),
// "think" and "thought" (a reasoning pass, by "pass", began and ended),
// "summary" (a line of a pass's summary as it stands: "pass", "part",
// "text"), "answer" (the entry begins), "usage" (the tokens spent,
// "usage"), "retry" ("text") — and {"text":…} lines, the entry's JSON a
// fragment at a time; a {"wait":seconds} line every few seconds besides, as
// a write takes a minute or two, longer than Cloudflare holds a silent
// request. The last line is the {"entry":…} (with "written" and the
// session's "tokens", and "corrected" when the lookup was a typo), or
// {"notfound":true,"suggestions":[…]} when the lookup is no word of the
// source language (that answer is not kept), or {"error":…}. "exact":true
// in the body skips the spelling pass and looks the text up as typed. Two
// lookups of one entry share one session, the later one reading every line
// from the start — a plain lookup only a session whose spelling it can
// trust, an exact one only a session writing its text as typed (the rules
// on dictFlight); the "writing" line's "q" is the word the session writes.
// A session outlives the window that started it, and what it writes is
// kept for the next lookup.
package server

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"exe/internal/dictlabels"
)

const (
	dictModel  = "gpt-6.1-sol"
	dictEffort = "xhigh"
	// a session that has not answered by then is stopped
	dictTimeout = 10 * time.Minute
	// how often a waiting lookup hears that the session is still writing
	dictTick = 5 * time.Second
	// a finished flight stays on the shared streams this long, for a
	// window whose start answered after the flight was done
	dictKeepFinished = 2 * time.Minute
	dictMaxQuery     = 80 // characters
)

// errNoLLM is what a lookup the dictionary cannot answer says on a host
// with no Codex CLI.
var errNoLLM = errors.New("No Usable LLM Backend")

// dictCodexPath finds the Codex CLI the entries are written with, "" when
// this host has none. Tests point it at a stand-in.
var dictCodexPath = func() string { return agentPath(hostAgents["codex"]) }

// A dictLang is one language a lookup can go from or to.
type dictLang struct {
	Code, Name string
}

// The languages looked up from, and the languages an entry is written in.
var (
	dictSources = []dictLang{{"en", "English"}, {"de", "German"}, {"fr", "French"},
		{"es", "Spanish"}, {"it", "Italian"}, {"la", "Latin"}}
	dictTargets = []dictLang{{"zh", "Chinese"}, {"ja", "Japanese"}, {"ko", "Korean"}}
)

func findDictLang(list []dictLang, code string) (dictLang, bool) {
	for _, l := range list {
		if l.Code == code {
			return l, true
		}
	}
	return dictLang{}, false
}

// dictEntry is what the session writes, field for field the schema below.
type dictEntry struct {
	Found          bool          `json:"found"`
	Headword       string        `json:"headword"`
	Note           string        `json:"note"`
	Forms          string        `json:"forms"`
	Pronunciations []dictPron    `json:"pronunciations"`
	Senses         []dictSense   `json:"senses"`
	Examples       []dictExample `json:"examples"`
	Etymology      string        `json:"etymology"`
	Related        []dictRelated `json:"related"`
	Literary       dictLiterary  `json:"literary"`
	Suggestions    []string      `json:"suggestions"`
}

type dictPron struct {
	Label string `json:"label"`
	IPA   string `json:"ipa"`
}

type dictSense struct {
	POS        string `json:"pos"`
	Gloss      string `json:"gloss"`
	Definition string `json:"definition"`
}

type dictExample struct {
	Text        string `json:"text"`
	Translation string `json:"translation"`
}

type dictRelated struct {
	Word     string `json:"word"`
	Relation string `json:"relation"`
	Gloss    string `json:"gloss"`
}

type dictLiterary struct {
	Note   string      `json:"note"`
	Quotes []dictQuote `json:"quotes"`
}

type dictQuote struct {
	Text        string `json:"text"`
	Translation string `json:"translation"`
	Source      string `json:"source"`
}

// dictSchema holds the session's last message to an entry. Every object
// names all its properties as required and allows no others: the strict
// form structured output asks for.
const dictSchema = `{"type":"object","additionalProperties":false,
"required":["found","headword","note","forms","pronunciations","senses","examples","etymology","related","literary","suggestions"],
"properties":{
"found":{"type":"boolean"},
"headword":{"type":"string"},
"note":{"type":"string"},
"forms":{"type":"string"},
"pronunciations":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["label","ipa"],
  "properties":{"label":{"type":"string"},"ipa":{"type":"string"}}}},
"senses":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["pos","gloss","definition"],
  "properties":{"pos":{"type":"string"},"gloss":{"type":"string"},"definition":{"type":"string"}}}},
"examples":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["text","translation"],
  "properties":{"text":{"type":"string"},"translation":{"type":"string"}}}},
"etymology":{"type":"string"},
"related":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["word","relation","gloss"],
  "properties":{"word":{"type":"string"},"relation":{"type":"string"},"gloss":{"type":"string"}}}},
"literary":{"type":"object","additionalProperties":false,"required":["note","quotes"],"properties":{
  "note":{"type":"string"},
  "quotes":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["text","translation","source"],
    "properties":{"text":{"type":"string"},"translation":{"type":"string"},"source":{"type":"string"}}}}}},
"suggestions":{"type":"array","items":{"type":"string"}}}}`

// Only English entries use the fixed English label grammar. Other source
// languages retain their dictionary's part-of-speech conventions.
func dictEntrySchema(src string) string {
	if src != "en" {
		return dictSchema
	}
	pos, _ := json.Marshal(map[string]string{"type": "string", "pattern": dictlabels.Pattern})
	return strings.Replace(dictSchema, `"pos":{"type":"string"}`, `"pos":`+string(pos), 1)
}

// dictTargetStyle is how an entry reads in each language it is written
// in: the language named for the writer, the separator between short
// equivalents, and the words a learner's dictionary in it labels
// relations with.
var dictTargetStyle = map[string]struct{ name, sep, relations string }{
	"zh": {"Simplified Chinese (简体中文)", "；", "同义词, 反义词, 派生词, 同源词, 复合词"},
	"ja": {"Japanese (日本語)", "、", "類義語, 対義語, 派生語, 同源語, 複合語"},
	"ko": {"Korean (한국어)", ", ", "유의어, 반의어, 파생어, 동원어, 합성어"},
}

// dictSourceStyle is what an entry of each source language must carry:
// the forms its dictionaries give and the pronunciations they print. The
// pronunciation labels are in the target language.
var dictSourceStyle = map[string]struct {
	forms string
	prons map[string][]string
}{
	"en": {"irregular plurals, past tenses and participles, irregular comparatives",
		map[string][]string{"zh": {"英", "美"}, "ja": {"英", "米"}, "ko": {"영", "미"}}},
	"de": {"for a noun its article, genitive and plural (der Hund; -(e)s, -e); for a verb its principal parts and auxiliary (gehen, ging, ist gegangen); irregular comparison",
		nil},
	"fr": {"the gender of a noun (n.m., n.f.) and an irregular plural; the feminine of an adjective; a verb's group, or its irregular stems",
		nil},
	"es": {"the gender of a noun and an irregular plural; a verb's irregularities (stem changes, irregular preterite, irregular participle)",
		nil},
	"it": {"the gender of a noun and an irregular plural; a verb's auxiliary (avere, essere) and an irregular past participle or past absolute",
		nil},
	"la": {"for a noun its nominative, genitive, gender and declension (amīcus, -ī, m., 2nd decl.); for a verb its four principal parts and conjugation (amō, amāre, amāvī, amātum, 1st conj.); for an adjective its nominative forms",
		map[string][]string{"zh": {"古典", "教会"}, "ja": {"古典", "教会"}, "ko": {"고전", "교회"}}},
}

// dictPrompt is the whole request a session gets: what to write and how,
// field by field. It says outright that no tool is to be used, and that a
// global AGENTS.md (which Codex reads into every session, ephemeral or
// not) has nothing to say here.
func dictPrompt(src, dst dictLang, q string) string {
	ts, ss := dictTargetStyle[dst.Code], dictSourceStyle[src.Code]
	pron := "one item with the standard pronunciation, labelled \"\""
	if labels := ss.prons[dst.Code]; len(labels) == 2 {
		pron = fmt.Sprintf("two items, labelled %q and %q", labels[0], labels[1])
		if src.Code == "en" {
			pron += " (British and American)"
		} else if src.Code == "la" {
			pron += " (Classical and Ecclesiastical Latin)"
		}
	}
	spelling := ""
	if src.Code == "la" {
		spelling = " Mark long vowels with macrons in the headword and forms; examples and quotations keep their ordinary spelling."
	}
	if src.Code == "en" {
		spelling = " Except senses[].pos: keep its English abbreviations as specified below."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "This is a dictionary request, not a coding task. Do not run commands, read files or call any tool, and no instructions from AGENTS.md files apply to it. Answer from your own knowledge with the JSON object alone.\n\n")
	fmt.Fprintf(&b, "Write one entry of a %[1]s–%[2]s dictionary for a reader whose language is %[2]s. Write every gloss, definition, explanation, label and translation in %[3]s; write the %[1]s side — headword, forms, examples, related words, quotations — in %[1]s, spelled as a good printed dictionary spells it.%[4]s\n\n",
		src.Name, dst.Name, ts.name, spelling)
	fmt.Fprintf(&b, "The reader looked up: %q\n\n", q)
	fmt.Fprintf(&b, "Fields:\n")
	fmt.Fprintf(&b, "- found: false when the lookup is not a %[1]s word or phrase (a misspelling, gibberish, a word of another language). Then leave every other field empty, except suggestions: up to five %[1]s words the reader may have meant. Otherwise true, and suggestions is [].\n", src.Name)
	fmt.Fprintf(&b, "- headword: the dictionary form, with its normal capitalisation.\n")
	fmt.Fprintf(&b, "- note: when the lookup is an inflected or variant form of the headword (a past tense, a plural, a comparative, a declined or conjugated form, an old spelling), one short sentence in %s saying which form of the headword it is; otherwise \"\".\n", dst.Name)
	fmt.Fprintf(&b, "- forms: the grammatical facts a learner needs, on one line, in the conventions of a %s dictionary: %s; \"\" when there is nothing to say.\n", src.Name, ss.forms)
	fmt.Fprintf(&b, "- pronunciations: IPA between slashes; %s.\n", pron)
	posStyle := fmt.Sprintf("the part of speech, abbreviated as a %s learner's dictionary of %s abbreviates it", dst.Name, src.Name)
	if src.Code == "en" {
		posStyle = "the part of speech in the English abbreviation format specified below"
	}
	fmt.Fprintf(&b, "- senses: the meanings, the commonest first, at most six. pos: %s. gloss: the short %s equivalents, separated by %q. definition: one or two sentences in %s saying exactly what the word means in that sense.\n", posStyle, dst.Name, ts.sep, dst.Name)
	if src.Code == "en" {
		fmt.Fprintln(&b, dictlabels.Instructions())
	}
	fmt.Fprintf(&b, "- examples: exactly two sentences in %[1]s using the word, each with a natural %[2]s translation. The first is simple: short and everyday. The second is complex: a longer sentence with subordinate clauses in a formal or literary register.\n", src.Name, dst.Name)
	fmt.Fprintf(&b, "- etymology: a short paragraph in %s tracing the word to its origin: the forms it came through, written in their own spelling (Greek in Greek letters with a transliteration), the language and period of each, and who coined it and when, if known.\n", dst.Name)
	fmt.Fprintf(&b, "- related: up to eight related %[1]s words — synonyms, antonyms, derivatives, cognates, compounds. relation: a short label in %[2]s (%[3]s…). gloss: its short %[2]s meaning.\n", src.Name, dst.Name, ts.relations)
	fmt.Fprintf(&b, "- literary.note: two or three sentences in %s on how the word lives in literature: its register and period, its connotations, writers known for it.\n", dst.Name)
	fmt.Fprintf(&b, "- literary.quotes: up to two passages from published literature that use the word, quoted word for word in %s, each with a %s translation and its source as \"Author, Work (year)\". Quote only passages you know verbatim; a quotation you are unsure of is worse than none, so give fewer or none rather than invent or paraphrase one.\n", src.Name, dst.Name)
	return b.String()
}

// dictKey is the form a lookup is kept under: trimmed, its runs of space
// made one, lower case — "Serendipity " and "serendipity" are one entry.
func dictKey(q string) string {
	return strings.ToLower(strings.Join(strings.Fields(q), " "))
}

// dictQuery reads and checks the three things a lookup names.
func dictQuery(from, to, q string) (src, dst dictLang, key string, err error) {
	var ok bool
	if src, ok = findDictLang(dictSources, from); !ok {
		return src, dst, "", fmt.Errorf("unknown source language %q", from)
	}
	if dst, ok = findDictLang(dictTargets, to); !ok {
		return src, dst, "", fmt.Errorf("unknown target language %q", to)
	}
	key = dictKey(q)
	if key == "" {
		return src, dst, "", errors.New("nothing to look up")
	}
	if !utf8.ValidString(key) || utf8.RuneCountInString(key) > dictMaxQuery {
		return src, dst, "", fmt.Errorf("a lookup is at most %d characters", dictMaxQuery)
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return src, dst, "", errors.New("a lookup is one line of text")
		}
	}
	return src, dst, key, nil
}

// dictKept is an entry as the dictionary keeps it, and as the API sends it.
type dictKept struct {
	From    string          `json:"from"`
	To      string          `json:"to"`
	Q       string          `json:"q"`
	Entry   json.RawMessage `json:"entry"`
	Model   string          `json:"model"`
	Effort  string          `json:"effort"`
	Created int64           `json:"created"` // unix ms
	// Written is set on the answer of the lookup that wrote it, with what
	// the session spent.
	Written bool       `json:"written,omitempty"`
	Tokens  *dictUsage `json:"tokens,omitempty"`
	// Corrected is what the reader typed, when it was a typo of Q.
	Corrected string `json:"corrected,omitempty"`
}

func (s *Server) dictDatabase() (*sql.DB, error) {
	s.dictMu.Lock()
	defer s.dictMu.Unlock()
	if s.dictDB != nil {
		return s.dictDB, nil
	}
	db, err := sql.Open("sqlite", filepath.Join(s.StateDir, "dict.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// typos: what a reader typed in a source language, and the word the
	// spelling pass read it as — never a page of its own
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS entries (
		src TEXT NOT NULL, dst TEXT NOT NULL, key TEXT NOT NULL,
		headword TEXT NOT NULL, entry TEXT NOT NULL,
		model TEXT NOT NULL, effort TEXT NOT NULL,
		created INTEGER NOT NULL, looked INTEGER NOT NULL, lookups INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (src, dst, key));
	CREATE TABLE IF NOT EXISTS typos (
		src TEXT NOT NULL, key TEXT NOT NULL, word TEXT NOT NULL, created INTEGER NOT NULL,
		PRIMARY KEY (src, key))`); err != nil {
		db.Close()
		return nil, err
	}
	s.dictDB = db
	return db, nil
}

// dictGet reads a kept entry and counts the lookup; nil when there is none.
func (s *Server) dictGet(src, dst, key string) (*dictKept, error) {
	db, err := s.dictDatabase()
	if err != nil {
		return nil, err
	}
	k := &dictKept{From: src, To: dst, Q: key}
	var entry string
	err = db.QueryRow(`SELECT entry, model, effort, created FROM entries WHERE src = ? AND dst = ? AND key = ?`,
		src, dst, key).Scan(&entry, &k.Model, &k.Effort, &k.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k.Entry = json.RawMessage(entry)
	db.Exec(`UPDATE entries SET looked = ?, lookups = lookups + 1 WHERE src = ? AND dst = ? AND key = ?`,
		time.Now().UnixMilli(), src, dst, key)
	return k, nil
}

func (s *Server) dictPut(k *dictKept, headword string) error {
	db, err := s.dictDatabase()
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR REPLACE INTO entries (src, dst, key, headword, entry, model, effort, created, looked, lookups)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		k.From, k.To, k.Q, headword, string(k.Entry), k.Model, k.Effort, k.Created, k.Created)
	return err
}

// dictTypo is the word a known typo stands for, "" when key is none.
func (s *Server) dictTypo(src, key string) string {
	db, err := s.dictDatabase()
	if err != nil {
		return ""
	}
	var word string
	db.QueryRow(`SELECT word FROM typos WHERE src = ? AND key = ?`, src, key).Scan(&word)
	return word
}

func (s *Server) dictPutTypo(src, key, word string) {
	if db, err := s.dictDatabase(); err == nil {
		db.Exec(`INSERT OR REPLACE INTO typos (src, key, word, created) VALUES (?, ?, ?, ?)`,
			src, key, word, time.Now().UnixMilli())
	}
}

// dictFind is the kept entry a lookup reads: its own, or — unless exact —
// the one for the word it is a known typo of. word is the key the entry is
// or would be kept under.
func (s *Server) dictFind(src, dst, key string, exact bool) (k *dictKept, word string, err error) {
	if k, err = s.dictGet(src, dst, key); k != nil || err != nil {
		return k, key, err
	}
	if !exact {
		if w := s.dictTypo(src, key); w != "" && w != key {
			if k, err = s.dictGet(src, dst, w); k != nil {
				k.Corrected = key
			}
			return k, w, err
		}
	}
	return nil, key, nil
}

func (s *Server) handleDictGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	src, dst, key, err := dictQuery(q.Get("from"), q.Get("to"), q.Get("q"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	k, _, err := s.dictFind(src.Code, dst.Code, key, q.Get("exact") == "1")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if k == nil {
		writeErr(w, http.StatusNotFound, errors.New("not in the dictionary"))
		return
	}
	writeJSON(w, http.StatusOK, k)
}

// A dictFlight is one session writing one entry, for every lookup of it.
// What the session says as it works — a step, a fragment of the entry —
// goes into events, in order: a lookup that joins late reads them all from
// the start, then follows the rest as they come.
//
// Flights are listed in s.dictFlights under ids that say what they write
// and whether its spelling is settled, so a lookup joins only work it can
// share. A spelling flight for a text T (dictPlain) is listed under
// spellID(T) for its whole life — a plain lookup of T joins it in any
// phase, the verdict comes in the replay — and under entryID(W) once the
// verdict names the word W it writes (T itself, or the word T was a typo
// of), where an exact lookup of W joins, and a plain lookup of W, or of a
// known typo of W. An exact flight (dictExact) is listed under entryID(T)
// alone and never spelled, so no plain lookup of T joins it: T may be a
// typo the reader insisted on. Two flights that resolve to one word do not
// write it twice: the later follows the earlier.
type dictFlight struct {
	began time.Time
	ids   []string // under which s.dictFlights lists it
	// fid names it on the shared streams, with the languages it writes in
	// and the word it set out to write; poke tells the streams it moved
	fid, from, to, word string
	poke                func()
	ended               time.Time // when it finished, under s.dictMu

	mu       sync.Mutex
	spelled  bool // its word is known to be one: a plain lookup may join it
	events   []map[string]any
	changed  chan struct{} // closed, and replaced, whenever the fields here move
	done     bool
	kept     *dictKept
	notFound []string // suggestions, when the lookup was no word
	err      error
}

// A dictMode is how a lookup wants its text read.
type dictMode int

const (
	dictPlain   dictMode = iota // spelling unknown: the pass first
	dictChecked                 // a known word (a typo's, from the table): no pass
	dictExact                   // as typed, whatever it is: no pass
)

// say adds one thing the session said.
func (f *dictFlight) say(ev map[string]any) {
	f.mu.Lock()
	f.events = append(f.events, ev)
	close(f.changed)
	f.changed = make(chan struct{})
	f.mu.Unlock()
	if f.poke != nil {
		f.poke()
	}
}

// finish sets how the session ended; set the result fields first.
func (f *dictFlight) finish() {
	f.mu.Lock()
	f.done = true
	close(f.changed)
	f.changed = make(chan struct{})
	f.mu.Unlock()
	if f.poke != nil {
		f.poke()
	}
}

// final is the line a flight ends on, for a reader who typed key: the
// entry (marked corrected when key was a typo of its word), the
// suggestions of a lookup that is no word, or what went wrong.
func (f *dictFlight) final(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.err != nil:
		return map[string]any{"error": f.err.Error()}
	case f.kept == nil:
		sugg := f.notFound
		if sugg == nil {
			sugg = []string{}
		}
		return map[string]any{"notfound": true, "suggestions": sugg}
	default:
		kept := *f.kept
		kept.Corrected = ""
		if key != "" && kept.Q != key {
			kept.Corrected = key // what this reader typed was a typo of it
		}
		return map[string]any{"entry": kept}
	}
}

// dictPoke wakes the shared streams: a flight started, said something or
// finished.
func (s *Server) dictPoke() {
	s.dictMu.Lock()
	if s.dictPoked != nil {
		close(s.dictPoked)
	}
	s.dictPoked = make(chan struct{})
	s.dictMu.Unlock()
}

// follow makes g's work f's own: g's events, as they come, on f's clock
// and without g's spelling lines (f's readers had their own), then g's
// result.
func (f *dictFlight) follow(g *dictFlight) {
	offset := g.began.Sub(f.began).Seconds()
	floor := 0.0
	f.mu.Lock()
	for _, ev := range f.events {
		if st, ok := ev["step"].(map[string]any); ok {
			if t, ok := st["t"].(float64); ok {
				floor = t
			}
		}
	}
	f.mu.Unlock()
	next := 0
	for {
		g.mu.Lock()
		evs, done, changed := g.events[next:], g.done, g.changed
		next = len(g.events)
		g.mu.Unlock()
		for _, ev := range evs {
			if st, ok := ev["step"].(map[string]any); ok {
				switch st["kind"] {
				case "session", "spell", "spelled":
					continue
				}
				cp := make(map[string]any, len(st))
				for k, v := range st {
					cp[k] = v
				}
				if t, ok := st["t"].(float64); ok {
					floor = math.Max(floor, math.Round((t+offset)*10)/10)
					cp["t"] = floor
				}
				ev = map[string]any{"step": cp}
			}
			f.say(ev)
		}
		if done {
			f.kept, f.notFound, f.err = g.kept, g.notFound, g.err
			return
		}
		<-changed
	}
}

// dictBegin reads a lookup's body and answers it as far as it can at once:
// a kept entry (k), or the flight that writes it (f, joined or started),
// with key, the reader's own text as kept, and word, what the flight
// writes. On a failure it has written the error itself and returns ok
// false.
func (s *Server) dictBegin(w http.ResponseWriter, r *http.Request) (k *dictKept, f *dictFlight, key, word string, ok bool) {
	var req struct {
		From, To, Q string
		// Exact looks the text up as typed: no spelling pass, no typo
		// read as the word it stands for.
		Exact bool
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	src, dst, key, err := dictQuery(req.From, req.To, req.Q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	k, word, err = s.dictFind(src.Code, dst.Code, key, req.Exact)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if k != nil {
		return k, nil, key, word, true
	}
	mode := dictPlain
	switch {
	case req.Exact:
		mode = dictExact
	case word != key:
		mode = dictChecked // a known typo: its word, checked already
	}
	if f, err = s.dictJoin(src, dst, word, mode); err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	return nil, f, key, word, true
}

// handleDictStart is a lookup that does not wait: a kept entry as
// {"entry":…}, or the flight that writes it as {"flight": id, "key",
// "q" (the word it writes), "model", "effort", "wait"} — its lines come on
// the window's shared stream (handleDictStream), and a window follows as
// many flights as it likes on that one connection.
func (s *Server) handleDictStart(w http.ResponseWriter, r *http.Request) {
	k, f, key, word, ok := s.dictBegin(w, r)
	if !ok {
		return
	}
	if k != nil {
		writeJSON(w, http.StatusOK, map[string]any{"entry": k})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flight": f.fid, "key": key, "q": word,
		"model": dictModel, "effort": dictEffort, "wait": int(time.Since(f.began).Seconds())})
}

// handleDictStream is a window's one connection to every session: every
// flight running, and those finished in the last minutes, each announced
// as {"flight": id, "began": {"from","to","q","wait"}}, then each of its
// lines as POST /v1/dict sends them with "flight" added, and its last line
// (entry, notfound or error) the same way — the entry unmarked, as the
// window knows what its tab typed. {"ready": true} follows the first
// round, the flights running when the window connected; a tab following
// a flight not among them (finished long ago, or lost to a daemon restart)
// starts its lookup again. {"ping": seconds} keeps a quiet stream open
// past Cloudflare's hundred silent seconds.
func (s *Server) handleDictStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	type cursor struct {
		next int
		done bool
	}
	seen := map[*dictFlight]*cursor{}
	tick := time.NewTicker(dictTick)
	defer tick.Stop()
	ready := false
	for {
		s.dictMu.Lock()
		kept := s.dictAll[:0]
		for _, f := range s.dictAll { // a flight long finished leaves the streams
			if f.ended.IsZero() || time.Since(f.ended) < dictKeepFinished {
				kept = append(kept, f)
			}
		}
		s.dictAll = kept
		all := append([]*dictFlight(nil), s.dictAll...)
		if s.dictPoked == nil {
			s.dictPoked = make(chan struct{})
		}
		poked := s.dictPoked
		s.dictMu.Unlock()
		for _, f := range all {
			c := seen[f]
			if c == nil {
				c = &cursor{}
				seen[f] = c
				enc.Encode(map[string]any{"flight": f.fid, "began": map[string]any{
					"from": f.from, "to": f.to, "q": f.word, "wait": int(time.Since(f.began).Seconds())}})
			}
			if c.done {
				continue
			}
			f.mu.Lock()
			evs, done := f.events[c.next:], f.done
			c.next = len(f.events)
			f.mu.Unlock()
			for _, ev := range evs {
				line := make(map[string]any, len(ev)+1)
				for k, v := range ev {
					line[k] = v
				}
				line["flight"] = f.fid
				enc.Encode(line)
			}
			if done {
				c.done = true
				line := f.final("")
				line["flight"] = f.fid
				enc.Encode(line)
			}
		}
		for f := range seen { // gone from the list: forget it
			if !slices.Contains(all, f) {
				delete(seen, f)
			}
		}
		if !ready {
			ready = true
			enc.Encode(map[string]any{"ready": true})
		}
		if fl != nil {
			fl.Flush()
		}
		select {
		case <-r.Context().Done():
			return // the sessions go on; what they write is kept
		case <-poked:
		case <-tick.C:
			enc.Encode(map[string]any{"ping": time.Now().Unix()})
		}
	}
}

func (s *Server) handleDictLookup(w http.ResponseWriter, r *http.Request) {
	k, f, key, word, ok := s.dictBegin(w, r)
	if !ok {
		return
	}
	fl, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	flush := func() {
		if fl != nil {
			fl.Flush()
		}
	}
	start := func() {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
	}
	if k != nil {
		start()
		enc.Encode(map[string]any{"entry": k})
		return
	}
	start()
	since := func() int { return int(time.Since(f.began).Seconds()) }
	enc.Encode(map[string]any{"writing": true, "model": dictModel, "effort": dictEffort, "wait": since(), "q": word})
	flush()
	tick := time.NewTicker(dictTick)
	defer tick.Stop()
	next := 0
	for {
		f.mu.Lock()
		evs, done, changed := f.events[next:], f.done, f.changed
		next = len(f.events)
		f.mu.Unlock()
		for _, ev := range evs {
			enc.Encode(ev)
		}
		if done {
			enc.Encode(f.final(key))
			flush()
			return
		}
		flush()
		select {
		case <-r.Context().Done():
			return // the session goes on; what it writes is kept
		case <-tick.C:
			enc.Encode(map[string]any{"wait": since()})
		case <-changed:
		}
	}
}

// dictJoin is the flight writing src→dst key for a lookup of the given
// mode: one already at it that the lookup can share (the rules on
// dictFlight), else one started now. errNoLLM when there is no Codex CLI
// to start one with.
func (s *Server) dictJoin(src, dst dictLang, key string, mode dictMode) (*dictFlight, error) {
	spellID := func(k string) string { return "spell\x00" + src.Code + "\x00" + dst.Code + "\x00" + k }
	entryID := func(k string) string { return "entry\x00" + src.Code + "\x00" + dst.Code + "\x00" + k }
	s.dictMu.Lock()
	defer s.dictMu.Unlock()
	switch mode {
	case dictExact:
		if f := s.dictFlights[entryID(key)]; f != nil {
			return f, nil
		}
	case dictChecked:
		if f := s.dictFlights[entryID(key)]; f != nil {
			return f, nil
		}
		if f := s.dictFlights[spellID(key)]; f != nil {
			return f, nil
		}
	default:
		if f := s.dictFlights[spellID(key)]; f != nil {
			return f, nil
		}
		if f := s.dictFlights[entryID(key)]; f != nil && f.spelled {
			return f, nil
		}
	}
	bin := dictCodexPath()
	if bin == "" {
		return nil, errNoLLM
	}
	if s.dictFlights == nil {
		s.dictFlights = map[string]*dictFlight{}
	}
	if s.dictBoot == "" {
		s.dictBoot = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	s.dictSeq++
	f := &dictFlight{began: time.Now(), changed: make(chan struct{}), spelled: mode == dictChecked,
		fid: s.dictBoot + "-" + strconv.Itoa(s.dictSeq), from: src.Code, to: dst.Code, word: key, poke: s.dictPoke}
	s.dictAll = append(s.dictAll, f)
	id := entryID(key)
	if mode == dictPlain {
		id = spellID(key)
	}
	f.ids = []string{id}
	s.dictFlights[id] = f
	// file lists f under the entry id of the word its spelling settled on;
	// when another flight holds it, that one is returned for f to follow
	file := func(word string) *dictFlight {
		s.dictMu.Lock()
		defer s.dictMu.Unlock()
		if g := s.dictFlights[entryID(word)]; g != nil && g != f {
			return g
		}
		s.dictFlights[entryID(word)] = f
		f.ids = append(f.ids, entryID(word))
		f.mu.Lock()
		f.spelled = true
		f.mu.Unlock()
		return nil
	}
	go func() {
		defer func() {
			s.dictMu.Lock()
			for _, id := range f.ids {
				if s.dictFlights[id] == f {
					delete(s.dictFlights, id)
				}
			}
			f.ended = time.Now()
			s.dictMu.Unlock()
			f.finish()
		}()
		// no ceiling: every lookup that needs a session gets one at once
		ctx, cancel := context.WithTimeout(context.Background(), dictTimeout)
		defer cancel()
		// kept meanwhile: by a session that ended between this lookup's
		// miss and its join — or, for a plain lookup, read as a typo
		// meanwhile
		if k, word, err := s.dictFind(src.Code, dst.Code, key, mode == dictExact); err == nil && k != nil {
			f.kept = k
			return
		} else if word != key {
			key, mode = word, dictChecked
		}
		step := func(kind string, more map[string]any) {
			st := map[string]any{"t": math.Round(time.Since(f.began).Seconds()*10) / 10, "kind": kind}
			for k, v := range more {
				st[k] = v
			}
			f.say(map[string]any{"step": st})
		}
		app, err := startCodexAppServer(ctx, bin)
		if err != nil {
			f.err = err
			return
		}
		defer app.close()
		step("session", nil)

		if mode == dictPlain {
			// the spelling pass: a typo is read as the word it stands for,
			// which is what gets looked up and kept; a lookup that is no
			// word ends here, with suggestions, before the long session
			step("spell", nil)
			v, err := dictSpell(ctx, app, src, key)
			if err != nil {
				f.err = err
				return
			}
			switch v.Verdict {
			case "unknown":
				step("spelled", map[string]any{"verdict": "unknown", "typed": key})
				f.notFound = v.Suggestions
				return
			case "typo":
				word := dictKey(v.Word)
				if word == "" || word == key || utf8.RuneCountInString(word) > dictMaxQuery {
					step("spelled", map[string]any{"verdict": "word", "typed": key})
					break
				}
				// word is the key it is kept under; shown, the word as the
				// pass spelled it, for the window (a German noun keeps its
				// capital)
				step("spelled", map[string]any{"verdict": "typo", "word": word, "typed": key,
					"shown": strings.Join(strings.Fields(v.Word), " ")})
				s.dictPutTypo(src.Code, key, word)
				key = word
				if k, err := s.dictGet(src.Code, dst.Code, key); err == nil && k != nil {
					f.kept = k
					return
				}
			default:
				step("spelled", map[string]any{"verdict": "word", "typed": key})
			}
		}
		if mode != dictExact {
			if g := file(key); g != nil {
				// another flight is writing this word: its work is f's
				f.follow(g)
				return
			}
		}

		final, usage, err := app.turn(ctx, dictPrompt(src, dst, key), dictEffort, "detailed", dictEntrySchema(src.Code), step,
			func(delta string) { f.say(map[string]any{"text": delta}) })
		if err != nil {
			f.err = err
			return
		}
		e, raw, err := dictParse(src.Code, final)
		if err != nil {
			f.err = err
			return
		}
		if !e.Found {
			f.notFound = e.Suggestions
			return
		}
		kept := &dictKept{From: src.Code, To: dst.Code, Q: key, Entry: raw,
			Model: dictModel, Effort: dictEffort, Created: time.Now().UnixMilli()}
		if err := s.dictPut(kept, e.Headword); err != nil {
			f.err = err
			return
		}
		written := *kept
		written.Written = true
		written.Tokens = usage
		f.kept = &written
	}()
	return f, nil
}

// dictParse reads a session's answer as an entry, and the entry in the
// schema's own shape, whatever spacing the model chose.
func dictParse(src, final string) (*dictEntry, json.RawMessage, error) {
	var e dictEntry
	if err := json.Unmarshal([]byte(final), &e); err != nil {
		return nil, nil, fmt.Errorf("Codex wrote no entry: %v", err)
	}
	if e.Found && (strings.TrimSpace(e.Headword) == "" || len(e.Senses) == 0) {
		return nil, nil, errors.New("Codex wrote an empty entry")
	}
	if e.Found && src == "en" {
		for i := range e.Senses {
			pos, err := dictlabels.Normalize(e.Senses[i].POS)
			if err != nil {
				return nil, nil, fmt.Errorf("Codex wrote an invalid label in sense %d: %w", i+1, err)
			}
			e.Senses[i].POS = pos
		}
	}
	norm, err := json.Marshal(e)
	if err != nil {
		return nil, nil, err
	}
	return &e, norm, nil
}

// The spelling pass runs before an entry is written: a quick turn, on the
// same model at medium effort (two to four seconds), that tells a word
// from a typo of one and from neither. Measured on nineteen lookups in
// four languages, low and medium agreed on every one: serendipty, recieve,
// Weltschmertz, beacoup and amorr read as typos; petrichor, irregardless,
// colour, Strasse, ging and amavit as words; xqzt as neither.
const (
	dictSpellEffort = "medium"
	dictSpellSchema = `{"type":"object","additionalProperties":false,"required":["verdict","word","suggestions"],
"properties":{"verdict":{"type":"string","enum":["word","typo","unknown"]},"word":{"type":"string"},
"suggestions":{"type":"array","items":{"type":"string"}}}}`
)

type dictSpelling struct {
	Verdict     string   `json:"verdict"`
	Word        string   `json:"word"`
	Suggestions []string `json:"suggestions"`
}

func dictSpellPrompt(src dictLang, q string) string {
	n := src.Name
	return "This is a spelling check, not a coding task. Do not run commands, read files or call any tool, and no instructions from AGENTS.md files apply to it. Answer from your own knowledge with the JSON object alone.\n\n" +
		fmt.Sprintf("A reader is about to look something up in a %s dictionary. They typed: %q\n\n", n, q) +
		fmt.Sprintf("- verdict \"word\": it is %[1]s as typed — a word, an inflected form, a phrase, a name, however rare, old, technical, informal or regional, or a word typed without its accents, umlauts or macrons, or in other capitals. word: the text as typed. suggestions: [].\n", n) +
		fmt.Sprintf("- verdict \"typo\": it is not %[1]s as typed, and it is plainly a slip in typing exactly one %[1]s word or phrase — a letter dropped, doubled, swapped or mistyped. word: that word, spelled right. suggestions: [].\n", n) +
		fmt.Sprintf("- verdict \"unknown\": it is neither — not %[1]s, and no one word it obviously stands for. word: \"\". suggestions: up to five %[1]s words the reader may have meant, the likeliest first.\n\n", n) +
		"Never take a real word for a typo of a commoner one. When in doubt between \"word\" and \"typo\", answer \"word\"."
}

func dictSpell(ctx context.Context, app *codexAppServer, src dictLang, q string) (*dictSpelling, error) {
	final, _, err := app.turn(ctx, dictSpellPrompt(src, q), dictSpellEffort, "none", dictSpellSchema, nil, nil)
	if err != nil {
		return nil, err
	}
	var v dictSpelling
	if err := json.Unmarshal([]byte(final), &v); err != nil {
		return nil, fmt.Errorf("Codex answered no spelling: %v", err)
	}
	return &v, nil
}

// dictUsage is what a session spent, as Codex counts it.
type dictUsage struct {
	Input     int64 `json:"inputTokens"`
	Cached    int64 `json:"cachedInputTokens"`
	Output    int64 `json:"outputTokens"`
	Reasoning int64 `json:"reasoningOutputTokens"`
	Total     int64 `json:"totalTokens"`
}

// dictFeaturesOff are the Codex features a dictionary session goes without:
// its shells, the apps and plugins it would start MCP servers for, and the
// user's hooks. Codex refuses a name it does not know, so only names it has
// are listed.
var dictFeaturesOff = []string{"shell_tool", "unified_exec", "apps", "plugins", "hooks"}

// codexAppServer is one `codex app-server` spoken to over stdio (JSON-RPC,
// a line a message) rather than codex exec, because the app server tells
// what a session is doing while it does it: each reasoning pass beginning
// and ending, each summary line of it as it is written, the answer as it
// streams, the tokens spent. One process runs a lookup's turns one after
// another — the spelling pass, then the entry — each on an ephemeral
// thread of its own: never saved, never in the resume list, read-only,
// approval-free, in an empty folder.
type codexAppServer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	enc    *json.Encoder
	sc     *bufio.Scanner
	stderr *tailBuffer
	work   string
	id     int
}

type codexRPCError struct {
	Message string `json:"message"`
}

type codexMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *codexRPCError  `json:"error"`
}

func startCodexAppServer(ctx context.Context, bin string) (*codexAppServer, error) {
	work, err := os.MkdirTemp("", "exe-dict-")
	if err != nil {
		return nil, err
	}
	args := []string{"app-server"}
	for _, f := range dictFeaturesOff {
		args = append(args, "--disable", f)
	}
	a := &codexAppServer{work: work, stderr: &tailBuffer{}}
	a.cmd = exec.CommandContext(ctx, bin, args...)
	a.cmd.Dir = work
	a.cmd.Env = cliEnv(bin)
	a.cmd.Stderr = a.stderr
	if a.stdin, err = a.cmd.StdinPipe(); err != nil {
		os.RemoveAll(work)
		return nil, err
	}
	stdout, err := a.cmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(work)
		return nil, err
	}
	if err := a.cmd.Start(); err != nil {
		os.RemoveAll(work)
		return nil, fmt.Errorf("Codex: %v", err)
	}
	a.enc = json.NewEncoder(a.stdin)
	a.sc = bufio.NewScanner(stdout)
	a.sc.Buffer(make([]byte, 64<<10), 16<<20)
	a.send(map[string]any{"id": a.nextID(), "method": "initialize", "params": map[string]any{
		"clientInfo": map[string]any{"name": "exe-dict", "title": "exe Dict", "version": "1"}}})
	if _, err := a.await(ctx, a.id, nil); err != nil {
		a.close()
		return nil, err
	}
	a.send(map[string]any{"method": "initialized"})
	return a, nil
}

func (a *codexAppServer) nextID() int { a.id++; return a.id }

func (a *codexAppServer) send(v any) { a.enc.Encode(v) }

// close ends the app server with its input, and kills one that lingers.
func (a *codexAppServer) close() {
	a.stdin.Close()
	done := make(chan struct{})
	go func() { a.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		a.cmd.Process.Kill()
		<-done
	}
	os.RemoveAll(a.work)
}

// failed is why a session went wrong, in Codex's own words where it said any.
func (a *codexAppServer) failed(ctx context.Context, why string) error {
	if ctx.Err() != nil {
		return errors.New("Codex took too long to write the entry")
	}
	if why == "" {
		why = codexComplaint(a.stderr.String())
	}
	if why == "" {
		why = "the session ended without an answer"
	}
	return fmt.Errorf("Codex: %s", why)
}

// await reads messages until the answer to request id, handing every
// notification on the way to note (nil drops them) and refusing any
// request of the server's own — an approval, a question: this session has
// nothing to approve and no one to ask.
func (a *codexAppServer) await(ctx context.Context, id int, note func(codexMsg) (bool, error)) (json.RawMessage, error) {
	want := strconv.Itoa(id)
	for a.sc.Scan() {
		var m codexMsg
		if json.Unmarshal(a.sc.Bytes(), &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			a.send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "not supported by exe Dict"}})
		case m.Method == "":
			if string(m.ID) != want {
				continue
			}
			if m.Error != nil {
				return nil, a.failed(ctx, m.Error.Message)
			}
			return m.Result, nil
		case note != nil:
			if stop, err := note(m); err != nil || stop {
				return nil, err
			}
		}
	}
	return nil, a.failed(ctx, "")
}

// turn runs one prompt on a fresh ephemeral thread and returns the answer,
// held to schema. step hears each reasoning pass begin and end ("think",
// "thought"), each line of its summary as it stands ("summary"), the
// answer begin ("answer"), the tokens spent ("usage") and a retry; text
// hears the answer a fragment at a time. Either may be nil.
func (a *codexAppServer) turn(ctx context.Context, prompt, effort, summary, schema string,
	step func(kind string, more map[string]any), text func(string)) (string, *dictUsage, error) {
	if step == nil {
		step = func(string, map[string]any) {}
	}
	if text == nil {
		text = func(string) {}
	}
	a.send(map[string]any{"id": a.nextID(), "method": "thread/start", "params": map[string]any{
		"ephemeral": true, "model": dictModel, "cwd": a.work,
		"sandbox": "read-only", "approvalPolicy": "never"}})
	res, err := a.await(ctx, a.id, nil)
	if err != nil {
		return "", nil, err
	}
	var started struct {
		Thread struct{ ID string } `json:"thread"`
	}
	json.Unmarshal(res, &started)
	if started.Thread.ID == "" {
		return "", nil, a.failed(ctx, "no thread was started")
	}
	a.send(map[string]any{"id": a.nextID(), "method": "turn/start", "params": map[string]any{
		"threadId": started.Thread.ID,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
		"effort":   effort, "summary": summary, "outputSchema": json.RawMessage(schema)}})

	var (
		answer   strings.Builder
		final    string
		usage    *dictUsage
		passes   = map[string]int{} // reasoning item → its pass number, from 1
		summ     = map[string]string{}
		lastErr  string
		finished bool
	)
	note := func(m codexMsg) (bool, error) {
		switch m.Method {
		case "item/started", "item/completed":
			var p struct {
				Item struct {
					Type, ID, Text string
				} `json:"item"`
			}
			json.Unmarshal(m.Params, &p)
			switch {
			case p.Item.Type == "reasoning" && m.Method == "item/started":
				passes[p.Item.ID] = len(passes) + 1
				step("think", map[string]any{"pass": passes[p.Item.ID]})
			case p.Item.Type == "reasoning":
				step("thought", map[string]any{"pass": passes[p.Item.ID]})
			case p.Item.Type == "agentMessage" && m.Method == "item/started":
				step("answer", nil)
			case p.Item.Type == "agentMessage":
				final = p.Item.Text
			}
		case "item/reasoning/summaryTextDelta":
			var p struct {
				ItemID       string `json:"itemId"`
				SummaryIndex int    `json:"summaryIndex"`
				Delta        string `json:"delta"`
			}
			json.Unmarshal(m.Params, &p)
			k := p.ItemID + "/" + strconv.Itoa(p.SummaryIndex)
			summ[k] += p.Delta
			step("summary", map[string]any{"pass": passes[p.ItemID], "part": p.SummaryIndex,
				"text": strings.TrimSpace(strings.ReplaceAll(summ[k], "**", ""))})
		case "item/agentMessage/delta":
			var p struct{ Delta string }
			json.Unmarshal(m.Params, &p)
			answer.WriteString(p.Delta)
			text(p.Delta)
		case "thread/tokenUsage/updated":
			var p struct {
				TokenUsage struct{ Total dictUsage } `json:"tokenUsage"`
			}
			json.Unmarshal(m.Params, &p)
			u := p.TokenUsage.Total
			usage = &u
			step("usage", map[string]any{"usage": u})
		case "error":
			var p struct {
				Error     codexRPCError
				WillRetry bool `json:"willRetry"`
			}
			json.Unmarshal(m.Params, &p)
			if p.WillRetry {
				step("retry", map[string]any{"text": p.Error.Message})
			} else {
				lastErr = p.Error.Message
			}
		case "turn/completed":
			var p struct {
				Turn struct {
					Status string
					Error  *codexRPCError
				}
			}
			json.Unmarshal(m.Params, &p)
			if p.Turn.Status != "completed" {
				why := lastErr
				if p.Turn.Error != nil && p.Turn.Error.Message != "" {
					why = p.Turn.Error.Message
				}
				if why == "" {
					why = "the session " + p.Turn.Status
				}
				return true, a.failed(ctx, why)
			}
			finished = true
			return true, nil
		}
		return false, nil
	}
	// the turn's own answer comes first; its notifications follow until
	// turn/completed, which ends the wait (await never sees an id -1)
	if _, err := a.await(ctx, a.id, note); err != nil {
		return "", nil, err
	}
	if !finished {
		if _, err := a.await(ctx, -1, note); err != nil {
			return "", nil, err
		}
	}
	if !finished {
		return "", nil, a.failed(ctx, lastErr)
	}
	if final == "" {
		final = answer.String()
	}
	return final, usage, nil
}

// codexComplaint picks what a failed session said: its last line naming
// an error, else its last line.
func codexComplaint(log string) string {
	lines := strings.Split(strings.TrimSpace(log), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.Contains(strings.ToLower(l), "error") {
			return clipText(l, 300)
		}
	}
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return clipText(l, 300)
	}
	return ""
}

// tailBuffer keeps the last 8 KB written to it.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if over := len(t.b) - 8<<10; over > 0 {
		t.b = bytes.Clone(t.b[over:])
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }
