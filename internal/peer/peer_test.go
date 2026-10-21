package peer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManifestBumpApplyPersist(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	v1 := m.Bump("Todo/todos.json", "nodeA", false, 10, 111)
	if v1.Vec["nodeA"] != 1 {
		t.Fatalf("bump: %+v", v1)
	}
	v2 := m.Bump("Todo/todos.json", "nodeA", false, 12, 222)
	if v2.Vec["nodeA"] != 2 {
		t.Fatalf("bump must advance: %+v", v2)
	}
	if !m.Fingerprint("Todo/todos.json", 12, 222) || m.Fingerprint("Todo/todos.json", 12, 999) {
		t.Fatal("fingerprint")
	}
	// adopt a remote version verbatim (a merged pointwise-max vector)
	m.Apply("Notes/notes.json", Version{Vec: map[string]int64{"nodeA": 2, "nodeB": 7}}, 5, 333)
	// reload from disk
	m2, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := m2.Get("Todo/todos.json"); !ok || v.Vec["nodeA"] != 2 {
		t.Fatalf("persist: %+v %v", v, ok)
	}
	if v, ok := m2.Get("Notes/notes.json"); !ok || v.Vec["nodeB"] != 7 || v.Vec["nodeA"] != 2 {
		t.Fatalf("persist applied: %+v %v", v, ok)
	}
}

func TestManifestMigratesLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	// a pre-vector manifest with the old {ctr, origin} shape
	legacy := `{"files":{"Todo/todos.json":{"ctr":5,"origin":"nodeX","deleted":false,"mtime":123,"size":9,"disk_mtime":456}}}`
	if err := os.WriteFile(filepath.Join(dir, "sync-manifest.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := m.Get("Todo/todos.json")
	if !ok || v.Vec["nodeX"] != 5 {
		t.Fatalf("legacy ctr/origin must migrate to a one-entry vector: %+v", v)
	}
}

func TestIdentityStable(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || a.PubKey() != b.PubKey() {
		t.Fatal("identity must be stable across loads")
	}
	if err := verifyClaimedKey(a.ID, a.PubKey()); err != nil {
		t.Fatal(err)
	}
	if err := verifyClaimedKey("0000000000000000", a.PubKey()); err == nil {
		t.Fatal("wrong id must not verify")
	}
}

func TestSignedRequestRoundtrip(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	idA, _ := LoadIdentity(dirA)
	idB, _ := LoadIdentity(dirB)
	store, err := LoadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Upsert(&Peer{ID: idA.ID, Name: "a", PubKey: idA.PubKey(), Addr: "127.0.0.1:1"})

	// idB is the recipient; every signed request is bound to idB.ID.
	body := []byte(`{"hello":true}`)
	req := httptest.NewRequest("PUT", "/v1/peer/file/Todo/todos.json", nil)
	req.Header.Set(hdrVer, EncodeVector(Version{Vec: map[string]int64{idA.ID: 3}}))
	SignRequest(idA, req, body, idB.ID)
	p, err := VerifyRequest(store, req, body, idB.ID)
	if err != nil || p.ID != idA.ID {
		t.Fatalf("verify: %v", err)
	}
	// tampered body
	if _, err := VerifyRequest(store, req, []byte(`{"hello":false}`), idB.ID); err == nil {
		t.Fatal("tampered body must fail")
	}
	// tampered version header (it's part of the canonical string)
	orig := req.Header.Get(hdrVer)
	req.Header.Set(hdrVer, EncodeVector(Version{Vec: map[string]int64{idA.ID: 4}}))
	if _, err := VerifyRequest(store, req, body, idB.ID); err == nil {
		t.Fatal("tampered version must fail")
	}
	req.Header.Set(hdrVer, orig)
	// re-targeted to a different node: a signature meant for idB must not
	// verify at some other daemon (replay via a relaying peer)
	if _, err := VerifyRequest(store, req, body, "someothernode"); err == nil {
		t.Fatal("request addressed to idB must fail at another node")
	}
	// stale timestamp
	req2 := httptest.NewRequest("GET", "/v1/peer/ping", nil)
	SignRequest(idA, req2, nil, idB.ID)
	req2.Header.Set(hdrTS, strconv.FormatInt(time.Now().Add(-10*time.Minute).UnixMilli(), 10))
	if _, err := VerifyRequest(store, req2, nil, idB.ID); err == nil {
		t.Fatal("stale timestamp must fail")
	}
	// unknown signer
	req3 := httptest.NewRequest("GET", "/v1/peer/ping", nil)
	SignRequest(idB, req3, nil, idB.ID)
	if _, err := VerifyRequest(store, req3, nil, idB.ID); err == nil {
		t.Fatal("unenrolled peer must fail")
	}
}

func TestStoreUpsertKeyPinning(t *testing.T) {
	dir := t.TempDir()
	idA, _ := LoadIdentity(t.TempDir())
	idB, _ := LoadIdentity(t.TempDir())
	s, err := LoadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(&Peer{ID: idA.ID, Name: "a", PubKey: idA.PubKey(), Addr: "x:1"}); err != nil {
		t.Fatal(err)
	}
	// same id, different key: must be rejected (trust-on-enroll)
	if err := s.Upsert(&Peer{ID: idA.ID, Name: "evil", PubKey: idB.PubKey()}); err == nil {
		t.Fatal("key swap must be rejected")
	}
	// addr/name refresh with the same key is fine
	if err := s.Upsert(&Peer{ID: idA.ID, Name: "renamed", PubKey: idA.PubKey(), Addr: "y:2"}); err != nil {
		t.Fatal(err)
	}
	s2, _ := LoadStore(dir)
	p, ok := s2.Get(idA.ID)
	if !ok || p.Name != "renamed" || p.Addr != "y:2" {
		t.Fatalf("persist: %+v", p)
	}
}

func TestApplyRemoteRejectsTraversalKeys(t *testing.T) {
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	// plant a file the traversal would target
	victim := filepath.Join(dir, "config.json")
	os.WriteFile(victim, []byte("original"), 0o644)
	rv := Version{Vec: map[string]int64{"ffff": 9}}
	rvDel := Version{Vec: map[string]int64{"ffff": 9}, Deleted: true}
	for _, key := range []string{"../config.json", "..\\config.json", "Todo/../../config.json", "noslashkey"} {
		if _, err := eng.ApplyRemote(key, []byte("pwned"), rv); err == nil {
			t.Fatalf("key %q must be rejected", key)
		}
		if _, err := eng.ApplyRemote(key, nil, rvDel); err == nil {
			t.Fatalf("delete key %q must be rejected", key)
		}
	}
	if got, _ := os.ReadFile(victim); string(got) != "original" {
		t.Fatalf("traversal escaped DataDir: victim is now %q", got)
	}
}

func TestApplyRemoteV1RemoteDoesNotClobberV2Local(t *testing.T) {
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	v2 := todoJSON(`{"id":"keep","text":"real work","done":false,"created":1,"updated":5}`)
	p := filepath.Join(dir, "appdata", "Todo", "todos.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, v2, 0o644)

	// a legacy peer pushes a bare v1 array with a concurrent version
	v1 := []byte(`[{"text":"legacy","done":false,"created":"2026-01-01T00:00:00Z"}]`)
	outcome, err := eng.ApplyRemote("Todo/todos.json", v1, Version{Vec: map[string]int64{"ffffffffffffffff": 99}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "kept-live" {
		t.Fatalf("v1 remote should be kept-live (ours preserved), got %s", outcome)
	}
	if got, _ := os.ReadFile(p); !bytes.Contains(got, []byte("real work")) {
		t.Fatalf("v2 local was clobbered by v1 remote: %s", got)
	}
}

func TestConcurrentDeleteLosesToLiveData(t *testing.T) {
	// The hello scenario: a node has real data for a file that another node
	// independently deleted. The delete is concurrent (neither saw the
	// other), so the live data must survive rather than be tombstoned.
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	real := todoJSON(`{"id":"real","text":"do not lose me","done":false,"created":1,"updated":1}`)
	p := filepath.Join(dir, "appdata", "Todo", "todos.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, real, 0o644)

	// a concurrent delete from another node (a stale tombstone, high counter)
	del := Version{Vec: map[string]int64{"othernode": 7}, Deleted: true}
	outcome, err := eng.ApplyRemote("Todo/todos.json", nil, del)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "kept-live" {
		t.Fatalf("concurrent delete must not win over live data, got %s", outcome)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("live file was deleted by a concurrent tombstone")
	}
	// but a delete that causally dominates our version DOES win
	local, _ := eng.man.Get("Todo/todos.json")
	dominating := Version{Vec: MaxVec(local.Vec, map[string]int64{"othernode": 8}), Deleted: true}
	// make it strictly dominate by bumping the other node past the merged vec
	dominating.Vec["othernode"] = local.Vec["othernode"] + 1
	for k, v := range local.Vec {
		if dominating.Vec[k] < v {
			dominating.Vec[k] = v
		}
	}
	if outcome, err := eng.ApplyRemote("Todo/todos.json", nil, dominating); err != nil || outcome != "applied" {
		t.Fatalf("a causally-dominating delete must win: outcome=%s err=%v", outcome, err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("dominating delete should have removed the file")
	}
}

func TestConcurrentNonMergeableBacksUpLoser(t *testing.T) {
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "appdata", "Paint", "canvas.png")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("LOCAL-ART"), 0o644) // scan-versioned on apply

	outcome, err := eng.ApplyRemote("Paint/canvas.png", []byte("REMOTE-ART"), Version{Vec: map[string]int64{"other": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "resolved" {
		t.Fatalf("non-mergeable concurrent conflict should resolve, got %s", outcome)
	}
	// the loser must be preserved under sync-conflicts, nothing silently lost
	conflicts := filepath.Join(dir, "sync-conflicts", "Paint")
	ents, err := os.ReadDir(conflicts)
	if err != nil || len(ents) != 1 {
		t.Fatalf("expected one conflict backup, got %v (err %v)", ents, err)
	}
	winner, _ := os.ReadFile(p)
	backup, _ := os.ReadFile(filepath.Join(conflicts, ents[0].Name()))
	// winner + loser are exactly the two inputs (deterministic by content hash)
	got := map[string]bool{string(winner): true, string(backup): true}
	if !got["LOCAL-ART"] || !got["REMOTE-ART"] {
		t.Fatalf("winner/backup should be the two inputs, got winner=%q backup=%q", winner, backup)
	}
}

func TestApplyRemoteVersionsUnseenLocalFile(t *testing.T) {
	// A push racing the first local scan must merge, not clobber: the
	// receiving node's pre-existing file has no manifest entry yet.
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	local := todoJSON(`{"id":"mine","text":"local","done":false,"created":1,"updated":1}`)
	p := filepath.Join(dir, "appdata", "Todo", "todos.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, local, 0o644)

	remote := todoJSON(`{"id":"theirs","text":"remote","done":false,"created":2,"updated":2}`)
	outcome, err := eng.ApplyRemote("Todo/todos.json", remote, Version{Vec: map[string]int64{"ffffffffffffffff": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "merged" {
		t.Fatalf("want merged, got %s", outcome)
	}
	got, _ := os.ReadFile(p)
	for _, want := range []string{"mine", "theirs"} {
		if !bytes.Contains(got, []byte(want)) {
			t.Fatalf("merged file lost %q: %s", want, got)
		}
	}
	// merged version is the pointwise max: our scan-bump {self:1} plus {ffff:1}
	if v, ok := eng.man.Get("Todo/todos.json"); !ok || v.Vec[ident.ID] != 1 || v.Vec["ffffffffffffffff"] != 1 {
		t.Fatalf("merged version should dominate both inputs: %+v", v)
	}
}

func newWorkspaceEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"),
		WorkspaceDir: filepath.Join(dir, "workspace"),
		Self:         ident, PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return eng, dir
}

func TestWorkspaceKeysRouteToWorkspaceDir(t *testing.T) {
	eng, dir := newWorkspaceEngine(t)
	outcome, err := eng.ApplyRemote("@workspace/pics/cat.png", []byte("CAT"), Version{Vec: map[string]int64{"other": 1}})
	if err != nil || outcome != "applied" {
		t.Fatalf("apply: outcome=%s err=%v", outcome, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "workspace", "pics", "cat.png"))
	if err != nil || string(got) != "CAT" {
		t.Fatalf("workspace file: %q err=%v", got, err)
	}
	// the namespace maps, it doesn't nest under appdata
	if _, err := os.Stat(filepath.Join(dir, "appdata", "@workspace")); !os.IsNotExist(err) {
		t.Fatal("workspace key leaked into appdata")
	}
	// a dominating delete tombstones it
	if outcome, err := eng.ApplyRemote("@workspace/pics/cat.png", nil,
		Version{Vec: map[string]int64{"other": 2}, Deleted: true}); err != nil || outcome != "applied" {
		t.Fatalf("delete: outcome=%s err=%v", outcome, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "workspace", "pics", "cat.png")); !os.IsNotExist(err) {
		t.Fatal("dominating delete should remove the workspace file")
	}
}

func TestWorkspaceKeysRejectedWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	rv := Version{Vec: map[string]int64{"other": 1}}
	if _, err := eng.ApplyRemote("@workspace/x.txt", []byte("x"), rv); err == nil {
		t.Fatal("workspace key must be rejected when WorkspaceDir is unset")
	}
}

func TestHiddenSegmentsNeverSync(t *testing.T) {
	eng, dir := newWorkspaceEngine(t)
	rv := Version{Vec: map[string]int64{"other": 1}}
	rvDel := Version{Vec: map[string]int64{"other": 1}, Deleted: true}
	for _, key := range []string{"@workspace/.Trash/doc.txt", "@workspace/sub/.git/config", "Todo/.hidden"} {
		if _, err := eng.ApplyRemote(key, []byte("x"), rv); err == nil {
			t.Fatalf("key %q must be rejected", key)
		}
		if _, err := eng.ApplyRemote(key, nil, rvDel); err == nil {
			t.Fatalf("delete key %q must be rejected", key)
		}
	}
	// the local-write hooks skip hidden paths too (a move into .Trash)
	trash := filepath.Join(dir, "workspace", ".Trash")
	os.MkdirAll(trash, 0o755)
	os.WriteFile(filepath.Join(trash, "doc.txt"), []byte("x"), 0o644)
	eng.LocalWrite(WorkspaceNS, ".Trash/doc.txt")
	if _, ok := eng.man.Get("@workspace/.Trash/doc.txt"); ok {
		t.Fatal("hidden path must not be versioned by LocalWrite")
	}
	eng.LocalDelete(WorkspaceNS, ".Trash/doc.txt")
	if _, ok := eng.man.Get("@workspace/.Trash/doc.txt"); ok {
		t.Fatal("hidden path must not be tombstoned by LocalDelete")
	}
}

func TestScanLocalInventoriesWorkspace(t *testing.T) {
	eng, dir := newWorkspaceEngine(t)
	ws := filepath.Join(dir, "workspace")
	os.MkdirAll(filepath.Join(ws, "sub"), 0o755)
	os.MkdirAll(filepath.Join(ws, ".Trash"), 0o755)
	os.MkdirAll(filepath.Join(dir, "appdata"), 0o755)
	os.WriteFile(filepath.Join(ws, "readme.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(ws, "sub", "a.bin"), []byte("aa"), 0o644)
	os.WriteFile(filepath.Join(ws, ".Trash", "gone.txt"), []byte("x"), 0o644)
	writeHuge(t, dir, "huge.bin")
	os.WriteFile(filepath.Join(dir, "appdata", "stray.txt"), []byte("x"), 0o644)

	eng.ScanLocal()
	for _, key := range []string{"@workspace/readme.txt", "@workspace/sub/a.bin"} {
		if v, ok := eng.man.Get(key); !ok || v.Deleted {
			t.Fatalf("scan should inventory %s: %+v %v", key, v, ok)
		}
	}
	for _, key := range []string{"@workspace/.Trash/gone.txt", "@workspace/huge.bin", "stray.txt"} {
		if _, ok := eng.man.Get(key); ok {
			t.Fatalf("scan must skip %s", key)
		}
	}
	// a workspace file removed on disk gets tombstoned on the next scan
	os.Remove(filepath.Join(ws, "readme.txt"))
	eng.ScanLocal()
	if v, ok := eng.man.Get("@workspace/readme.txt"); !ok || !v.Deleted {
		t.Fatalf("removed workspace file should tombstone: %+v %v", v, ok)
	}
	// the oversized file never tombstones either — it's seen, just not carried
	eng.ScanLocal()
	if _, ok := eng.man.Get("@workspace/huge.bin"); ok {
		t.Fatal("oversized file must stay out of the manifest")
	}
}

func TestWorkspaceFilesAreNotMergeable(t *testing.T) {
	if Mergeable("@workspace/todos.json") || Mergeable("@workspace/sub/notes.json") {
		t.Fatal("workspace files must sync whole-file, never item-merge")
	}
	if !Mergeable("Todo/todos.json") {
		t.Fatal("app todos.json must stay mergeable")
	}
}

func TestStrayWorkspaceFilesMigrateOnScan(t *testing.T) {
	// A pre-workspace binary that pulled from an upgraded peer landed
	// workspace files under appdata/@workspace/. The scan must move them into
	// the real workspace — NOT sweep their manifest keys as deletions.
	eng, dir := newWorkspaceEngine(t)
	stray := filepath.Join(dir, "appdata", WorkspaceNS, "pics")
	os.MkdirAll(stray, 0o755)
	os.WriteFile(filepath.Join(stray, "cat.png"), []byte("CAT"), 0o644)
	fi, _ := os.Stat(filepath.Join(stray, "cat.png"))
	eng.man.Apply("@workspace/pics/cat.png",
		Version{Vec: map[string]int64{"other": 1}}, fi.Size(), fi.ModTime().UnixNano())

	eng.ScanLocal()
	got, err := os.ReadFile(filepath.Join(dir, "workspace", "pics", "cat.png"))
	if err != nil || string(got) != "CAT" {
		t.Fatalf("stray should move into the workspace: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "appdata", WorkspaceNS)); !os.IsNotExist(err) {
		t.Fatal("emptied stray tree should be removed")
	}
	v, ok := eng.man.Get("@workspace/pics/cat.png")
	if !ok || v.Deleted {
		t.Fatalf("migrated file must stay live in the manifest: %+v %v", v, ok)
	}
	// the rename kept size+mtime, so the version must not have re-bumped
	if v.Vec["other"] != 1 || len(v.Vec) != 1 {
		t.Fatalf("migration should not re-version: %+v", v)
	}
}

// writeHuge puts a file one byte over the sync cap in the engine's workspace
// and returns its key and the fingerprint a stamp of it would record.
func writeHuge(t *testing.T, dir, rel string) (key string, size, mtime int64) {
	t.Helper()
	return writeSparse(t, dir, rel, maxSyncFile+1)
}

// writeSparse makes a workspace file of n bytes without writing them.
func writeSparse(t *testing.T, dir, rel string, n int64) (key string, size, mtime int64) {
	t.Helper()
	p := filepath.Join(dir, "workspace", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p, n); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return WorkspaceNS + "/" + rel, fi.Size(), fi.ModTime().UnixNano()
}

func TestLocalWriteLeavesOversizedFileLocal(t *testing.T) {
	// A workspace move calls LocalWrite on a file of any size; the PUT that
	// caps a write at 100 MB is not in its way.
	eng, dir := newWorkspaceEngine(t)
	key, _, _ := writeHuge(t, dir, "Disk Images/huge.toast")
	eng.LocalWrite(WorkspaceNS, "Disk Images/huge.toast")
	if _, ok := eng.man.Get(key); ok {
		t.Fatal("a file over the cap must not be versioned")
	}
	os.WriteFile(filepath.Join(dir, "workspace", "small.txt"), []byte("hello"), 0o644)
	eng.LocalWrite(WorkspaceNS, "small.txt")
	if v, ok := eng.man.Get("@workspace/small.txt"); !ok || v.Deleted {
		t.Fatalf("a file under the cap is versioned as before: %+v %v", v, ok)
	}
}

func TestOversizedFileIsNeitherPushedNorServed(t *testing.T) {
	// An entry can still name a file over the cap — one that grew, or one an
	// older build stamped. None of it goes on the wire, in either direction.
	eng, dir := newWorkspaceEngine(t)
	self := eng.cfg.Self.ID
	huge, size, mtime := writeHuge(t, dir, "huge.bin")
	eng.man.Bump(huge, self, false, size, mtime)
	os.WriteFile(filepath.Join(dir, "workspace", "small.txt"), []byte("hello"), 0o644)
	eng.man.Bump("@workspace/small.txt", self, false, 5, 1)

	if _, _, err := eng.FileForPeer(WorkspaceNS, "huge.bin", maxSyncFile); !errors.Is(err, errTooLarge) {
		t.Fatalf("serving a file over the cap: want errTooLarge, got %v", err)
	}
	if b, _, err := eng.FileForPeer(WorkspaceNS, "small.txt", maxSyncFile); err != nil || string(b) != "hello" {
		t.Fatalf("serving a file under the cap: %q %v", b, err)
	}

	var mu sync.Mutex
	var puts []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		puts = append(puts, r.Method+" "+r.URL.Path)
		mu.Unlock()
	}))
	defer ts.Close()
	p := &Peer{ID: "ffffffffffffffff", Name: "b", Addr: strings.TrimPrefix(ts.URL, "http://")}
	eng.pushTo(p, huge)
	eng.pushTo(p, "@workspace/small.txt")
	mu.Lock()
	defer mu.Unlock()
	if len(puts) != 1 || puts[0] != "PUT /v1/peer/file/@workspace/small.txt" {
		t.Fatalf("only the file under the cap is pushed, got %v", puts)
	}
}

func TestPushesWhatThePeerSaysItTakes(t *testing.T) {
	// A peer's manifest names the biggest file it takes; an older release
	// names none, and gets nothing over the 10 MB it always took.
	for _, tc := range []struct {
		name    string
		maxFile int64
		want    []string
	}{
		{"older release", 0, []string{"@workspace/small.txt"}},
		{"this release", maxSyncFile, []string{"@workspace/mid.bin", "@workspace/small.txt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng, dir := newWorkspaceEngine(t)
			self := eng.cfg.Self.ID
			mid, size, mtime := writeSparse(t, dir, "mid.bin", legacySyncFile+1)
			eng.man.Bump(mid, self, false, size, mtime)
			os.WriteFile(filepath.Join(dir, "workspace", "small.txt"), []byte("hello"), 0o644)
			eng.man.Bump("@workspace/small.txt", self, false, 5, 1)

			const id = "ffffffffffffffff"
			var mu sync.Mutex
			got := map[string]int64{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/peer/manifest" {
					m := map[string]any{"id": id, "files": map[string]Version{}}
					if tc.maxFile > 0 {
						m["max_file"] = tc.maxFile
					}
					json.NewEncoder(w).Encode(m)
					return
				}
				n, _ := io.Copy(io.Discard, r.Body)
				mu.Lock()
				got[strings.TrimPrefix(r.URL.Path, "/v1/peer/file/")] = n
				mu.Unlock()
			}))
			defer ts.Close()
			eng.reconcile(&Peer{ID: id, Name: "b", Addr: strings.TrimPrefix(ts.URL, "http://")})
			mu.Lock()
			defer mu.Unlock()
			keys := slices.Sorted(maps.Keys(got))
			if !slices.Equal(keys, tc.want) {
				t.Fatalf("pushed %v, want %v", keys, tc.want)
			}
			if n, ok := got[mid]; ok && n != size {
				t.Fatalf("mid.bin arrived with %d bytes of %d", n, size)
			}
		})
	}
}

func TestServesWhatTheAskerSaysItTakes(t *testing.T) {
	eng, dir := newWorkspaceEngine(t)
	mid, size, mtime := writeSparse(t, dir, "mid.bin", legacySyncFile+1)
	eng.man.Bump(mid, eng.cfg.Self.ID, false, size, mtime)

	ask := func(maxFile string) int64 {
		r := httptest.NewRequest("GET", "/v1/peer/file/@workspace/mid.bin", nil)
		if maxFile != "" {
			r.Header.Set(hdrMaxFile, maxFile)
		}
		return Takes(r)
	}
	for in, want := range map[string]int64{
		"": legacySyncFile, "lots": legacySyncFile, "-1": legacySyncFile,
		"4096": 4096, strconv.Itoa(maxSyncFile): maxSyncFile, "99999999999999": maxSyncFile,
	} {
		if got := ask(in); got != want {
			t.Errorf("X-Exe-Max-File %q takes %d, want %d", in, got, want)
		}
	}
	if _, _, err := eng.FileForPeer(WorkspaceNS, "mid.bin", ask("")); !errors.Is(err, errTooLarge) {
		t.Fatalf("an older release asking: want errTooLarge, got %v", err)
	}
	if b, _, err := eng.FileForPeer(WorkspaceNS, "mid.bin", ask(strconv.Itoa(maxSyncFile))); err != nil || int64(len(b)) != size {
		t.Fatalf("this release asking: %d bytes, %v", len(b), err)
	}
}

func TestFileTransferGivesUpOnlyWhenItStalls(t *testing.T) {
	stall, answer := stallTimeout, answerTimeout
	stallTimeout, answerTimeout = 200*time.Millisecond, 2*time.Second
	t.Cleanup(func() { stallTimeout, answerTimeout = stall, answer })
	eng, _ := newWorkspaceEngine(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/moving": // longer than the stall timeout, never still for it
			for range 8 {
				w.Write([]byte("chunk"))
				w.(http.Flusher).Flush()
				time.Sleep(40 * time.Millisecond)
			}
		case "/stalled":
			w.Write([]byte("chunk"))
			w.(http.Flusher).Flush()
			time.Sleep(800 * time.Millisecond)
			w.Write([]byte("late"))
		case "/put": // the whole body, then a slow answer
			io.Copy(io.Discard, r.Body)
			time.Sleep(400 * time.Millisecond)
			w.Write([]byte("saved"))
		}
	}))
	defer ts.Close()
	do := func(req *http.Request) (string, error) {
		resp, answer, done, err := eng.doFile(req)
		if err != nil {
			return "", err
		}
		defer done()
		defer resp.Body.Close()
		b, err := io.ReadAll(answer)
		return string(b), err
	}
	get := func(path string) (string, error) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		return do(req)
	}
	if b, err := get("/moving"); err != nil || b != strings.Repeat("chunk", 8) {
		t.Fatalf("a transfer that keeps moving outlasts the stall timeout: %q %v", b, err)
	}
	if b, err := get("/stalled"); err == nil {
		t.Fatalf("a transfer that stops moving is given up, got %q", b)
	}
	req, _ := http.NewRequest("PUT", ts.URL+"/put", bytes.NewReader([]byte("body")))
	if b, err := do(req); err != nil || b != "saved" {
		t.Fatalf("a push waits answerTimeout for its answer: %q %v", b, err)
	}
}

func TestScanDropsOversizedEntryNoPeerHolds(t *testing.T) {
	eng, dir := newWorkspaceEngine(t)
	self := eng.cfg.Self.ID
	os.MkdirAll(filepath.Join(dir, "appdata"), 0o755)

	// stamped once on a file over the cap, as LocalWrite used to: no peer
	// could take that version and there is none before it
	fresh, size, mtime := writeHuge(t, dir, "fresh.bin")
	eng.man.Bump(fresh, self, false, size, mtime)
	// written twice: a peer may hold the first, so the history stays
	twice, size, mtime := writeHuge(t, dir, "twice.bin")
	eng.man.Bump(twice, self, false, 5, 1)
	eng.man.Bump(twice, self, false, size, mtime)
	// stamped while small and grown since: the version the peers hold
	grown, _, _ := writeHuge(t, dir, "grown.bin")
	eng.man.Bump(grown, self, false, 5, 1)
	// resolved against a peer's version of the same file
	shared, size, mtime := writeHuge(t, dir, "shared.bin")
	eng.man.Apply(shared, Version{Vec: map[string]int64{self: 1, "ffffffffffffffff": 1}}, size, mtime)

	before := eng.man.Snapshot()
	eng.ScanLocal()
	if _, ok := eng.man.Get(fresh); ok {
		t.Fatal("an entry no peer can hold must be dropped, with no tombstone left")
	}
	if _, err := os.Stat(filepath.Join(dir, "workspace", "fresh.bin")); err != nil {
		t.Fatalf("the file itself stays: %v", err)
	}
	for _, key := range []string{twice, grown, shared} {
		if v, ok := eng.man.Get(key); !ok || !v.SameVersion(before[key]) {
			t.Fatalf("%s keeps its version %+v, got %+v %v", key, before[key], v, ok)
		}
	}
	reloaded, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get(fresh); ok {
		t.Fatal("the drop must be saved")
	}
	if _, ok := reloaded.Get(twice); !ok {
		t.Fatal("the kept entries must be saved with it")
	}
}
