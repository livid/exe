package peer

import (
	"bytes"
	"encoding/json"
	"path"
	"sort"
	"strings"
	"time"
)

// Item-level merging for the record-bearing app documents. Everything
// else syncs whole-file last-writer-wins; these union by record id with
// per-record updated stamps, so concurrent edits on two nodes both survive.
// The schemas mirror what the apps write (exe-apps Todo, Notes, World Clock):
//
//	todos.json   {"version":2,"items":[{id,text,done,created,updated,order?,deleted?}]}
//	notes.json   {"notes":[{id,text,created,updated,deleted?}]}
//	clocks.json  {"version":1,"items":[{id,name,region,tz,created,updated,deleted?}]}
//	             (World Clock only — the City app has a cities.json of its own shape)
//	drafts.json  {"version":2,"drafts":[{id,text,checked,created,updated,deleted?}]}
//	             (Blue Pencil only; checked maps a paragraph to its correction)
//	places.json  {"version":1,"items":[{id,name,region,country,cc,lat,lon,elev?,tz,pop?,feature,created,updated,deleted?}]}
//	             (Weather only — a city as Open-Meteo's geocoder describes it, keyed by its GeoNames id)
//
// deleted is a tombstone stamp (ms); merged output GCs tombstones older
// than 30 days — the same TTL the apps use. Merge output is canonical
// (sorted, MarshalIndent) and deterministic, so every node converges on
// byte-identical content. A document that fails to parse falls back to LWW.

const tombstoneTTL = 30 * 24 * time.Hour

// Mergeable reports whether key ("App/file") gets item-level merging. A
// workspace file that happens to be named todos.json is just a file — only
// app data carries the record schemas.
func Mergeable(key string) bool {
	if strings.HasPrefix(key, WorkspaceNS+"/") {
		return false
	}
	switch path.Base(key) {
	case "todos.json", "notes.json":
		return true
	}
	return key == clocksKey || key == draftsKey || key == placesKey
}

// clocksKey is the World Clock's city list; matched by full key since the
// file name alone is too generic to claim.
const clocksKey = "World Clock/clocks.json"

// MergeFile merges two versions of a mergeable document. ok is false when
// the file isn't mergeable or either side doesn't parse — callers fall back
// to LWW.
func MergeFile(key string, local, remote []byte) (merged []byte, ok bool) {
	switch path.Base(key) {
	case "todos.json":
		return mergeTodos(local, remote)
	case "notes.json":
		return mergeNotes(local, remote)
	}
	if key == clocksKey {
		return mergeClocks(local, remote)
	}
	if key == draftsKey {
		return mergeDrafts(local, remote)
	}
	if key == placesKey {
		return mergePlaces(local, remote)
	}
	return nil, false
}

// CanonicalFile is a document reduced to the deterministic merge form —
// what MergeFile would produce given two copies of it. Comparing merged
// output against canonical forms (never raw bytes) is what lets a JS-
// serialized doc and a Go-serialized doc count as equal.
func CanonicalFile(key string, data []byte) ([]byte, bool) {
	return MergeFile(key, data, data)
}

type todoItem struct {
	ID      string  `json:"id"`
	Text    string  `json:"text"`
	Done    bool    `json:"done"`
	Created int64   `json:"created"`
	Updated int64   `json:"updated"`
	Order   float64 `json:"order,omitempty"` // fractional drag-reorder rank; app falls back to created
	Deleted int64   `json:"deleted,omitempty"`
}

type todoDoc struct {
	Version int        `json:"version"`
	Items   []todoItem `json:"items"`
}

func parseTodos(b []byte) (*todoDoc, bool) {
	var d todoDoc
	if json.Unmarshal(b, &d) != nil || d.Version != 2 {
		return nil, false
	}
	for _, it := range d.Items {
		if it.ID == "" {
			return nil, false
		}
	}
	return &d, true
}

// pickJSON breaks an updated-stamp tie deterministically on the serialized
// records, so two nodes agree without a per-item origin.
func pickJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Compare(ab, bb) >= 0
}

// wins reports whether candidate should replace cur for the same id. Higher
// `updated` wins; on a tie a tombstone (deleted != 0) always beats a live
// record so a concurrent same-timestamp edit can never resurrect a deletion,
// and only then does the deterministic byte tiebreak decide.
func wins(candUpdated int64, candDeleted bool, curUpdated int64, curDeleted bool, cand, cur any) bool {
	if candUpdated != curUpdated {
		return candUpdated > curUpdated
	}
	if candDeleted != curDeleted {
		return candDeleted
	}
	return !pickJSON(cur, cand)
}

func mergeTodos(local, remote []byte) ([]byte, bool) {
	a, ok := parseTodos(local)
	if !ok {
		return nil, false
	}
	b, ok := parseTodos(remote)
	if !ok {
		return nil, false
	}
	m := map[string]todoItem{}
	for _, it := range a.Items {
		m[it.ID] = it
	}
	for _, it := range b.Items {
		old, seen := m[it.ID]
		if !seen || wins(it.Updated, it.Deleted != 0, old.Updated, old.Deleted != 0, it, old) {
			m[it.ID] = it
		}
	}
	now := time.Now().UnixMilli()
	items := make([]todoItem, 0, len(m))
	for _, it := range m {
		if it.Deleted != 0 && now-it.Deleted > tombstoneTTL.Milliseconds() {
			continue
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Created != items[j].Created {
			return items[i].Created < items[j].Created
		}
		return items[i].ID < items[j].ID
	})
	out, err := json.MarshalIndent(todoDoc{Version: 2, Items: items}, "", "  ")
	if err != nil {
		return nil, false
	}
	return out, true
}

type noteItem struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Created int64  `json:"created"`
	Updated int64  `json:"updated"`
	Deleted int64  `json:"deleted,omitempty"`
}

// noteDoc drops any legacy "sel" field on re-serialize: selection is
// per-viewer state that no longer belongs in the synced document.
type noteDoc struct {
	Notes []noteItem `json:"notes"`
}

func parseNotes(b []byte) (*noteDoc, bool) {
	var d noteDoc
	if json.Unmarshal(b, &d) != nil || d.Notes == nil {
		return nil, false
	}
	for _, n := range d.Notes {
		if n.ID == "" {
			return nil, false
		}
	}
	return &d, true
}

func mergeNotes(local, remote []byte) ([]byte, bool) {
	a, ok := parseNotes(local)
	if !ok {
		return nil, false
	}
	b, ok := parseNotes(remote)
	if !ok {
		return nil, false
	}
	m := map[string]noteItem{}
	for _, n := range a.Notes {
		m[n.ID] = n
	}
	for _, n := range b.Notes {
		old, seen := m[n.ID]
		if !seen || wins(n.Updated, n.Deleted != 0, old.Updated, old.Deleted != 0, n, old) {
			m[n.ID] = n
		}
	}
	now := time.Now().UnixMilli()
	notes := make([]noteItem, 0, len(m))
	for _, n := range m {
		if n.Deleted != 0 && now-n.Deleted > tombstoneTTL.Milliseconds() {
			continue
		}
		notes = append(notes, n)
	}
	// newest-edited first with id tiebreak: the app's display order, and
	// deterministic across nodes
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].Updated != notes[j].Updated {
			return notes[i].Updated > notes[j].Updated
		}
		return notes[i].ID < notes[j].ID
	})
	out, err := json.MarshalIndent(noteDoc{Notes: notes}, "", "  ")
	if err != nil {
		return nil, false
	}
	return out, true
}

// ---- World Clock ----

type clockItem struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"` // tombstones keep only id and stamps
	Region  string `json:"region,omitempty"`
	TZ      string `json:"tz,omitempty"`
	Created int64  `json:"created"`
	Updated int64  `json:"updated"`
	Deleted int64  `json:"deleted,omitempty"`
}

type clockDoc struct {
	Version int         `json:"version"`
	Items   []clockItem `json:"items"`
}

func parseClocks(b []byte) (*clockDoc, bool) {
	var d clockDoc
	if json.Unmarshal(b, &d) != nil || d.Version != 1 || d.Items == nil {
		return nil, false
	}
	for _, it := range d.Items {
		if it.ID == "" {
			return nil, false
		}
	}
	return &d, true
}

func mergeClocks(local, remote []byte) ([]byte, bool) {
	a, ok := parseClocks(local)
	if !ok {
		return nil, false
	}
	b, ok := parseClocks(remote)
	if !ok {
		return nil, false
	}
	m := map[string]clockItem{}
	for _, it := range a.Items {
		m[it.ID] = it
	}
	for _, it := range b.Items {
		old, seen := m[it.ID]
		if !seen || wins(it.Updated, it.Deleted != 0, old.Updated, old.Deleted != 0, it, old) {
			m[it.ID] = it
		}
	}
	now := time.Now().UnixMilli()
	items := make([]clockItem, 0, len(m))
	for _, it := range m {
		if it.Deleted != 0 && now-it.Deleted > tombstoneTTL.Milliseconds() {
			continue
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Created != items[j].Created {
			return items[i].Created < items[j].Created
		}
		return items[i].ID < items[j].ID
	})
	out, err := json.MarshalIndent(clockDoc{Version: 1, Items: items}, "", "  ")
	if err != nil {
		return nil, false
	}
	return out, true
}

// ---- Weather ----

// placesKey is the Weather app's city list; matched by full key since the
// file name alone is too generic to claim.
const placesKey = "Weather/places.json"

// placeItem carries every field the app writes (unknown keys are dropped
// on merge). Numbers are pointers so a sea-level elevation or an empty
// population survives as 0 rather than vanishing under omitempty.
type placeItem struct {
	ID      string   `json:"id"`
	Name    string   `json:"name,omitempty"` // tombstones keep only id and stamps
	Region  string   `json:"region,omitempty"`
	Country string   `json:"country,omitempty"`
	CC      string   `json:"cc,omitempty"`
	Lat     *float64 `json:"lat,omitempty"`
	Lon     *float64 `json:"lon,omitempty"`
	Elev    *float64 `json:"elev,omitempty"`
	TZ      string   `json:"tz,omitempty"`
	Pop     *int64   `json:"pop,omitempty"`
	Feature string   `json:"feature,omitempty"`
	Created int64    `json:"created"`
	Updated int64    `json:"updated"`
	Deleted int64    `json:"deleted,omitempty"`
}

type placeDoc struct {
	Version int         `json:"version"`
	Items   []placeItem `json:"items"`
}

func parsePlaces(b []byte) (*placeDoc, bool) {
	var d placeDoc
	if json.Unmarshal(b, &d) != nil || d.Version != 1 || d.Items == nil {
		return nil, false
	}
	for _, it := range d.Items {
		if it.ID == "" {
			return nil, false
		}
	}
	return &d, true
}

func mergePlaces(local, remote []byte) ([]byte, bool) {
	a, ok := parsePlaces(local)
	if !ok {
		return nil, false
	}
	b, ok := parsePlaces(remote)
	if !ok {
		return nil, false
	}
	m := map[string]placeItem{}
	for _, it := range a.Items {
		m[it.ID] = it
	}
	for _, it := range b.Items {
		old, seen := m[it.ID]
		if !seen || wins(it.Updated, it.Deleted != 0, old.Updated, old.Deleted != 0, it, old) {
			m[it.ID] = it
		}
	}
	now := time.Now().UnixMilli()
	items := make([]placeItem, 0, len(m))
	for _, it := range m {
		if it.Deleted != 0 && now-it.Deleted > tombstoneTTL.Milliseconds() {
			continue
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Created != items[j].Created {
			return items[i].Created < items[j].Created
		}
		return items[i].ID < items[j].ID
	})
	out, err := json.MarshalIndent(placeDoc{Version: 1, Items: items}, "", "  ")
	if err != nil {
		return nil, false
	}
	return out, true
}

// ---- Blue Pencil ----

// draftsKey is Blue Pencil's drafts column; matched by full key since the
// file name alone is too generic to claim.
const draftsKey = "BluePencil/drafts.json"

type draftItem struct {
	ID      string            `json:"id"`
	Text    string            `json:"text"`
	Checked map[string]string `json:"checked,omitempty"` // paragraph → the pencil's correction; tombstones drop it with the text
	Created int64             `json:"created"`
	Updated int64             `json:"updated"`
	Deleted int64             `json:"deleted,omitempty"`
}

type draftDoc struct {
	Version int         `json:"version"`
	Drafts  []draftItem `json:"drafts"`
}

func parseDrafts(b []byte) (*draftDoc, bool) {
	var d draftDoc
	if json.Unmarshal(b, &d) != nil || d.Drafts == nil {
		return nil, false
	}
	for _, x := range d.Drafts {
		if x.ID == "" {
			return nil, false
		}
	}
	return &d, true
}

func mergeDrafts(local, remote []byte) ([]byte, bool) {
	a, ok := parseDrafts(local)
	if !ok {
		return nil, false
	}
	b, ok := parseDrafts(remote)
	if !ok {
		return nil, false
	}
	m := map[string]draftItem{}
	for _, x := range a.Drafts {
		m[x.ID] = x
	}
	for _, x := range b.Drafts {
		old, seen := m[x.ID]
		if !seen || wins(x.Updated, x.Deleted != 0, old.Updated, old.Deleted != 0, x, old) {
			m[x.ID] = x
		}
	}
	now := time.Now().UnixMilli()
	drafts := make([]draftItem, 0, len(m))
	for _, x := range m {
		if x.Deleted != 0 {
			if now-x.Deleted > tombstoneTTL.Milliseconds() {
				continue
			}
			x.Text, x.Checked = "", nil // a tombstone keeps only id and stamps
		}
		drafts = append(drafts, x)
	}
	// newest-started first with id tiebreak: the column's order, and
	// deterministic across nodes
	sort.Slice(drafts, func(i, j int) bool {
		if drafts[i].Created != drafts[j].Created {
			return drafts[i].Created > drafts[j].Created
		}
		return drafts[i].ID < drafts[j].ID
	})
	version := a.Version
	if b.Version > version {
		version = b.Version
	}
	out, err := json.MarshalIndent(draftDoc{Version: version, Drafts: drafts}, "", "  ")
	if err != nil {
		return nil, false
	}
	return out, true
}
