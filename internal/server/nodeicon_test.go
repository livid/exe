package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"exe/internal/config"
)

// The set opens with exe's own computer and then the 64 to pick from,
// each with its lines in every desk language (the picker shows its name).
func TestNodeIconSet(t *testing.T) {
	if len(nodeIcons) != 65 || nodeIcons[0].ID != defaultNodeIcon {
		t.Fatalf("%d icons, first %q: want exe and 64 more", len(nodeIcons), nodeIcons[0].ID)
	}
	for _, lang := range config.Languages[1:] {
		cat := uiCatalog(t, lang)
		for _, ic := range nodeIcons[1:] {
			if cat[ic.Name] == "" {
				t.Errorf("%s: no line for the icon name %q", lang, ic.Name)
			}
		}
	}
	// a broken set is refused whole, not drawn wrong
	for _, bad := range []string{
		`{"icons":[]}`,
		`{"icons":[{"id":"floppy","name":"F","tile":"#ffffff","palette":{},"rows":[]}]}`,
		`{"icons":[{"id":"exe","name":"exe","tile":"#fff","palette":{},"rows":[]}]}`,
	} {
		if _, err := parseNodeIcons([]byte(bad)); err == nil {
			t.Errorf("parsed %s", bad)
		}
	}
}

// What a node serves for its icon files: the embedded ones, byte for byte,
// while it picks none; the picked art once it does — the touch icon and
// the maskable one opaque on the art's tile, the manifest's icons at
// addresses that name it — and the default again for an icon it lacks.
func TestNodeIconFiles(t *testing.T) {
	cfg := &config.Config{}
	s := New(cfg, nil, nil, "", t.TempDir())
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		return rec
	}
	for _, f := range []string{"icon.svg", "icon-192.png", "icon-512.png", "icon-maskable-512.png", "manifest.json"} {
		want, _ := uiFS.ReadFile("ui/" + f)
		if got := get("/ui/" + f).Body.Bytes(); !bytes.Equal(got, want) {
			t.Errorf("default %s is not the embedded file", f)
		}
	}
	defTag := get("/ui/icon-192.png").Header().Get("ETag")

	pick := nodeIcons[1]
	cfg.Icon = pick.ID
	sizes := map[string]int{"icon-192.png": 192, "icon-512.png": 512, "icon-maskable-512.png": 512, "apple-touch-icon.png": 180}
	for f, size := range sizes {
		rec := get("/ui/" + f)
		img, err := png.Decode(rec.Body)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if b := img.Bounds(); b != image.Rect(0, 0, size, size) {
			t.Errorf("%s is %v, want %d square", f, b, size)
		}
		corner := img.At(0, 0).(color4)
		_, _, _, a := corner.RGBA()
		onTile := f == "apple-touch-icon.png" || f == "icon-maskable-512.png"
		if onTile && (a != 0xffff || !sameRGB(corner, pick.tile)) {
			t.Errorf("%s: corner %v, want the tile %v", f, corner, pick.tile)
		}
		if !onTile && a != 0 {
			t.Errorf("%s: corner %v, want clear", f, corner)
		}
	}
	if tag := get("/ui/icon-192.png").Header().Get("ETag"); tag == defTag {
		t.Error("the picked icon has the default's tag")
	}
	svg := get("/ui/icon.svg")
	if ct := svg.Header().Get("Content-Type"); ct != "image/svg+xml" || !strings.HasPrefix(svg.Body.String(), "<svg ") {
		t.Errorf("icon.svg: %s %.40s", ct, svg.Body.String())
	}
	var m struct {
		Name  string `json:"name"`
		Icons []struct {
			Src string `json:"src"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(get("/ui/manifest.json").Body.Bytes(), &m); err != nil || m.Name != "exe" || len(m.Icons) != 4 {
		t.Fatalf("manifest: %v %+v", err, m)
	}
	for _, ic := range m.Icons {
		if !strings.HasSuffix(ic.Src, "?icon="+pick.ID) {
			t.Errorf("manifest icon %s does not name %s", ic.Src, pick.ID)
		}
	}
	// the other /ui/ files are still the embedded ones
	if rec := get("/ui/offline.html"); !strings.Contains(rec.Body.String(), "<html") {
		t.Error("offline.html is not served")
	}

	cfg.Icon = "no-such-icon"
	if got, want := get("/ui/icon-192.png").Body.Bytes(), mustUI(t, "ui/icon-192.png"); !bytes.Equal(got, want) {
		t.Error("an icon this node lacks did not draw the default")
	}
}

type color4 = interface{ RGBA() (r, g, b, a uint32) }

func sameRGB(c color4, want interface{ RGBA() (r, g, b, a uint32) }) bool {
	r1, g1, b1, _ := c.RGBA()
	r2, g2, b2, _ := want.RGBA()
	return r1 == r2 && g1 == g2 && b1 == b2
}

func mustUI(t *testing.T, name string) []byte {
	b, err := uiFS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Configuration saves the icon: the default as none, an icon of the set by
// its id, one the set lacks refused — unless the configuration already
// names it (a newer exe wrote it), so a Save of other fields still goes
// through.
func TestConfigIcon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("EXE_HOME", home)
	if filepath.Dir(config.Path()) != home {
		t.Fatalf("config path %s is not the test's", config.Path())
	}
	s := New(config.Default(), nil, nil, "", home)
	put := func(body string) int {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("PUT", "/v1/config", bytes.NewBufferString(body)))
		return rec.Code
	}
	id := nodeIcons[len(nodeIcons)-1].ID
	if code := put(`{"icon":" ` + strings.ToUpper(id) + `"}`); code != http.StatusOK || s.Config().Icon != id {
		t.Fatalf("pick %s: %d, saved %q", id, code, s.Config().Icon)
	}
	if code := put(`{"icon":"exe"}`); code != http.StatusOK || s.Config().Icon != "" {
		t.Fatalf("the default: %d, saved %q", code, s.Config().Icon)
	}
	if code := put(`{"icon":"no-such-icon"}`); code != http.StatusBadRequest {
		t.Errorf("an unknown icon: %d, want 400", code)
	}
	nc := *s.Config()
	nc.Icon = "from-a-newer-exe"
	if err := s.storeConfig(&nc); err != nil {
		t.Fatal(err)
	}
	if code := put(`{"icon":"from-a-newer-exe","default_cpus":3}`); code != http.StatusOK || s.Config().DefaultCPUs != 3 {
		t.Errorf("a save beside a newer exe's icon: %d", code)
	}
}
