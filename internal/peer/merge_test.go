package peer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func todoJSON(items ...string) []byte {
	return []byte(`{"version":2,"items":[` + join(items) + `]}`)
}

func join(items []string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += ","
		}
		out += it
	}
	return out
}

func TestMergeTodosUnionAndLWW(t *testing.T) {
	a := todoJSON(
		`{"id":"x","text":"shared old","done":false,"created":1,"updated":10}`,
		`{"id":"a","text":"only a","done":false,"created":2,"updated":5}`)
	b := todoJSON(
		`{"id":"x","text":"shared new","done":true,"created":1,"updated":20}`,
		`{"id":"b","text":"only b","done":false,"created":3,"updated":6}`)
	merged, ok := MergeFile("Todo/todos.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	var d todoDoc
	if err := json.Unmarshal(merged, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 3 {
		t.Fatalf("want 3 items, got %d: %s", len(d.Items), merged)
	}
	byID := map[string]todoItem{}
	for _, it := range d.Items {
		byID[it.ID] = it
	}
	if byID["x"].Text != "shared new" || !byID["x"].Done {
		t.Fatalf("LWW lost: %+v", byID["x"])
	}
	if _, ok := byID["a"]; !ok {
		t.Fatal("union lost a")
	}
	if _, ok := byID["b"]; !ok {
		t.Fatal("union lost b")
	}
	// commutative and canonical: both directions produce identical bytes
	rev, ok := MergeFile("Todo/todos.json", b, a)
	if !ok || !bytes.Equal(merged, rev) {
		t.Fatalf("merge not commutative:\n%s\n%s", merged, rev)
	}
	// idempotent: canonical form is a fixed point
	again, ok := MergeFile("Todo/todos.json", merged, merged)
	if !ok || !bytes.Equal(merged, again) {
		t.Fatal("canonical form is not a fixed point")
	}
}

func TestMergeTodosKeepsOrder(t *testing.T) {
	// drag-reorder writes a fractional `order` on one item; the merge must
	// carry it (a schema-stripping merge would silently undo every reorder)
	a := todoJSON(`{"id":"x","text":"moved","done":false,"created":1,"updated":20,"order":1.5}`)
	b := todoJSON(`{"id":"x","text":"moved","done":false,"created":1,"updated":10}`)
	merged, ok := MergeFile("Todo/todos.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	var d todoDoc
	json.Unmarshal(merged, &d)
	if len(d.Items) != 1 || d.Items[0].Order != 1.5 {
		t.Fatalf("order rank must survive the merge: %s", merged)
	}
}

func TestMergeTodosTombstoneWins(t *testing.T) {
	now := time.Now().UnixMilli()
	a := todoJSON(fmt.Sprintf(`{"id":"x","text":"edited","done":false,"created":1,"updated":%d}`, now-1000))
	b := todoJSON(fmt.Sprintf(`{"id":"x","text":"","done":false,"created":1,"updated":%d,"deleted":%d}`, now, now))
	merged, ok := MergeFile("Todo/todos.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	var d todoDoc
	json.Unmarshal(merged, &d)
	if len(d.Items) != 1 || d.Items[0].Deleted == 0 {
		t.Fatalf("tombstone should win: %s", merged)
	}
}

func TestMergeTombstoneWinsUpdatedTie(t *testing.T) {
	// A stripped (resurrected) copy and its tombstone tie on `updated`; the
	// tombstone must win regardless of byte order, or deletions come back.
	ts := time.Now().UnixMilli()
	live := todoJSON(fmt.Sprintf(`{"id":"x","text":"back from the dead","done":false,"created":1,"updated":%d}`, ts))
	tomb := todoJSON(fmt.Sprintf(`{"id":"x","text":"","done":false,"created":1,"updated":%d,"deleted":%d}`, ts, ts))
	for _, order := range [][2][]byte{{live, tomb}, {tomb, live}} {
		merged, ok := MergeFile("Todo/todos.json", order[0], order[1])
		if !ok {
			t.Fatal("merge not ok")
		}
		var d todoDoc
		json.Unmarshal(merged, &d)
		if len(d.Items) != 1 || d.Items[0].Deleted == 0 {
			t.Fatalf("tombstone must win the tie both ways: %s", merged)
		}
	}
}

func TestMergeTodosGCsOldTombstones(t *testing.T) {
	old := time.Now().Add(-40 * 24 * time.Hour).UnixMilli()
	a := todoJSON(fmt.Sprintf(`{"id":"x","text":"","done":false,"created":1,"updated":%d,"deleted":%d}`, old, old))
	b := todoJSON(`{"id":"y","text":"keep","done":false,"created":2,"updated":3}`)
	merged, ok := MergeFile("Todo/todos.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	var d todoDoc
	json.Unmarshal(merged, &d)
	if len(d.Items) != 1 || d.Items[0].ID != "y" {
		t.Fatalf("40-day tombstone should be gone: %s", merged)
	}
}

func TestMergeTodosV1FallsBackToLWW(t *testing.T) {
	v1 := []byte(`[{"text":"legacy","done":false,"created":"2026-01-01T00:00:00Z"}]`)
	v2 := todoJSON(`{"id":"x","text":"new","done":false,"created":1,"updated":2}`)
	if _, ok := MergeFile("Todo/todos.json", v1, v2); ok {
		t.Fatal("v1 array must not merge")
	}
	if _, ok := MergeFile("Paint/canvas.png", []byte("a"), []byte("b")); ok {
		t.Fatal("non-mergeable file must not merge")
	}
}

func TestMergeNotesDropsLegacySel(t *testing.T) {
	a := []byte(`{"notes":[{"id":"n1","text":"from a","created":1,"updated":10}],"sel":"n1"}`)
	b := []byte(`{"notes":[{"id":"n2","text":"from b","created":2,"updated":20}],"sel":"n2"}`)
	merged, ok := MergeFile("Notes/notes.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	if bytes.Contains(merged, []byte(`"sel"`)) {
		t.Fatalf("sel must not survive: %s", merged)
	}
	var d noteDoc
	json.Unmarshal(merged, &d)
	if len(d.Notes) != 2 {
		t.Fatalf("want 2 notes: %s", merged)
	}
	// newest-updated first, the app's display order
	if d.Notes[0].ID != "n2" {
		t.Fatalf("order should be updated-desc: %s", merged)
	}
}

func TestVersionVectorOrdering(t *testing.T) {
	v := func(m map[string]int64) Version { return Version{Vec: m} }
	base := v(map[string]int64{"a": 1})
	after := v(map[string]int64{"a": 2}) // a made another edit
	if !after.Dominates(base) || base.Dominates(after) {
		t.Fatal("a longer history must dominate")
	}
	if after.Concurrent(base) || base.Concurrent(after) {
		t.Fatal("causally-ordered versions are not concurrent")
	}
	// independent edits on two nodes: neither has seen the other
	x := v(map[string]int64{"a": 1})
	y := v(map[string]int64{"b": 1})
	if x.Dominates(y) || y.Dominates(x) {
		t.Fatal("independent edits must not dominate")
	}
	if !x.Concurrent(y) || !y.Concurrent(x) {
		t.Fatal("independent edits must be concurrent")
	}
	// the merge of both dominates each
	m := v(MaxVec(x.Vec, y.Vec))
	if !m.Dominates(x) || !m.Dominates(y) {
		t.Fatal("pointwise max must dominate both inputs")
	}
	if !after.SameVersion(v(map[string]int64{"a": 2})) {
		t.Fatal("SameVersion must ignore MTime and match equal vectors")
	}
}

func TestMergeClocksUnionTombstoneAndKey(t *testing.T) {
	// a fresh tombstone stamp, so the 30-day GC leaves it alone
	gone := fmt.Sprint(time.Now().UnixMilli())
	a := []byte(`{"version":1,"items":[` +
		`{"id":"Los Angeles|America/Los_Angeles","name":"Los Angeles","region":"CA","tz":"America/Los_Angeles","created":1,"updated":1},` +
		`{"id":"Tokyo|Asia/Tokyo","name":"Tokyo","region":"Japan","tz":"Asia/Tokyo","created":2,"updated":2}]}`)
	// the other node removed Tokyo later and added Paris
	b := []byte(`{"version":1,"items":[` +
		`{"id":"Los Angeles|America/Los_Angeles","name":"Los Angeles","region":"CA","tz":"America/Los_Angeles","created":1,"updated":1},` +
		`{"id":"Tokyo|Asia/Tokyo","created":2,"updated":` + gone + `,"deleted":` + gone + `},` +
		`{"id":"Paris|Europe/Paris","name":"Paris","region":"France","tz":"Europe/Paris","created":3,"updated":3}]}`)
	if !Mergeable("World Clock/clocks.json") {
		t.Fatal("clocks.json should merge")
	}
	if Mergeable("City/clocks.json") || Mergeable("City/cities.json") {
		t.Fatal("only the World Clock's file carries the schema")
	}
	merged, ok := MergeFile("World Clock/clocks.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	var d clockDoc
	if err := json.Unmarshal(merged, &d); err != nil {
		t.Fatal(err)
	}
	byID := map[string]clockItem{}
	for _, it := range d.Items {
		byID[it.ID] = it
	}
	if len(d.Items) != 3 || byID["Tokyo|Asia/Tokyo"].Deleted == 0 || byID["Tokyo|Asia/Tokyo"].Name != "" {
		t.Fatalf("tombstone lost: %s", merged)
	}
	if byID["Paris|Europe/Paris"].TZ != "Europe/Paris" || byID["Los Angeles|America/Los_Angeles"].Region != "CA" {
		t.Fatalf("fields stripped: %s", merged)
	}
	rev, ok := MergeFile("World Clock/clocks.json", b, a)
	if !ok || !bytes.Equal(merged, rev) {
		t.Fatalf("merge not commutative:\n%s\n%s", merged, rev)
	}
	again, ok := MergeFile("World Clock/clocks.json", merged, merged)
	if !ok || !bytes.Equal(merged, again) {
		t.Fatal("canonical form is not a fixed point")
	}
}

func TestMergePlacesUnionTombstoneAndKey(t *testing.T) {
	gone := fmt.Sprint(time.Now().UnixMilli()) // a tombstone inside its 30-day life
	la := `{"id":"geo:5368361","name":"Los Angeles","region":"California","country":"United States","cc":"US","lat":34.05223,"lon":-118.24368,"elev":89,"tz":"America/Los_Angeles","pop":3820914,"feature":"PPLA2","created":1,"updated":1}`
	a := []byte(`{"version":1,"items":[` + la + `,` +
		`{"id":"geo:1850147","name":"Tokyo","country":"Japan","cc":"JP","lat":35.6895,"lon":139.69171,"elev":0,"tz":"Asia/Tokyo","feature":"PPLC","created":2,"updated":2}]}`)
	// the other node removed Tokyo later and added a sea-level Amsterdam
	b := []byte(`{"version":1,"items":[` + la + `,` +
		`{"id":"geo:1850147","created":2,"updated":` + gone + `,"deleted":` + gone + `},` +
		`{"id":"geo:2759794","name":"Amsterdam","region":"North Holland","country":"Netherlands","cc":"NL","lat":52.37403,"lon":4.88969,"elev":0,"tz":"Europe/Amsterdam","pop":0,"feature":"PPLC","created":3,"updated":3}]}`)
	if !Mergeable("Weather/places.json") {
		t.Fatal("places.json should merge")
	}
	if Mergeable("City/places.json") || Mergeable("Workspace/places.json") {
		t.Fatal("only the Weather app's file carries the schema")
	}
	merged, ok := MergeFile("Weather/places.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	var d placeDoc
	if err := json.Unmarshal(merged, &d); err != nil {
		t.Fatal(err)
	}
	byID := map[string]placeItem{}
	for _, it := range d.Items {
		byID[it.ID] = it
	}
	if len(d.Items) != 3 || byID["geo:1850147"].Deleted == 0 || byID["geo:1850147"].Name != "" || byID["geo:1850147"].Lat != nil {
		t.Fatalf("tombstone lost: %s", merged)
	}
	ams := byID["geo:2759794"]
	if ams.Elev == nil || *ams.Elev != 0 || ams.Pop == nil || *ams.Pop != 0 || ams.TZ != "Europe/Amsterdam" {
		t.Fatalf("zero-valued fields stripped: %s", merged)
	}
	if l := byID["geo:5368361"]; l.Lat == nil || *l.Lat != 34.05223 || l.Pop == nil || *l.Pop != 3820914 || l.Region != "California" || l.CC != "US" {
		t.Fatalf("fields stripped: %s", merged)
	}
	rev, ok := MergeFile("Weather/places.json", b, a)
	if !ok || !bytes.Equal(merged, rev) {
		t.Fatalf("merge not commutative:\n%s\n%s", merged, rev)
	}
	again, ok := MergeFile("Weather/places.json", merged, merged)
	if !ok || !bytes.Equal(merged, again) {
		t.Fatal("canonical form is not a fixed point")
	}
}

func TestMergeDraftsUnionTombstoneAndKey(t *testing.T) {
	now := time.Now().UnixMilli() // a tombstone inside its 30-day life
	a := []byte(fmt.Sprintf(`{"version":2,"drafts":[
		{"id":"d1","text":"from a","checked":{"from a":"From A."},"created":1,"updated":10},
		{"id":"d2","created":2,"updated":%d,"deleted":%d}]}`, now, now))
	b := []byte(fmt.Sprintf(`{"version":2,"drafts":[
		{"id":"d1","text":"from a, edited","checked":{"from a, edited":"From A, edited."},"created":1,"updated":20},
		{"id":"d2","text":"resurrected?","created":2,"updated":%d},
		{"id":"d3","text":"from b","checked":{},"created":3,"updated":30}]}`, now))
	if Mergeable("Notes/drafts.json") {
		t.Fatal("drafts.json is Blue Pencil's alone")
	}
	if _, ok := MergeFile("Notes/drafts.json", a, b); ok {
		t.Fatal("a drafts.json elsewhere must fall back to LWW")
	}
	merged, ok := MergeFile("BluePencil/drafts.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	var d draftDoc
	if err := json.Unmarshal(merged, &d); err != nil {
		t.Fatal(err)
	}
	if d.Version != 2 || len(d.Drafts) != 3 {
		t.Fatalf("want version 2 and 3 drafts: %s", merged)
	}
	// newest-started first: d3, d2, d1
	if d.Drafts[0].ID != "d3" || d.Drafts[1].ID != "d2" || d.Drafts[2].ID != "d1" {
		t.Fatalf("order should be created-desc: %s", merged)
	}
	// the later edit wins and brings its checked paragraph along
	if d1 := d.Drafts[2]; d1.Text != "from a, edited" || d1.Checked["from a, edited"] != "From A, edited." || d1.Updated != 20 {
		t.Fatalf("d1 should be b's newer record with its checked map: %s", merged)
	}
	// a same-stamp tombstone beats the live record, and carries no text
	if d2 := d.Drafts[1]; d2.Deleted == 0 || d2.Text != "" || d2.Checked != nil {
		t.Fatalf("d2 should stay a bare tombstone: %s", merged)
	}
	// merging the result with itself is a fixed point
	again, ok := MergeFile("BluePencil/drafts.json", merged, merged)
	if !ok || !bytes.Equal(again, merged) {
		t.Fatalf("not canonical:\n%s\n%s", merged, again)
	}
}
