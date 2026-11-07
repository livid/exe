package server

// A node's icon. Someone who runs exe on several machines adds each desk
// to their phone's Home Screen, and every one of them wore exe's computer:
// Configuration → General now picks this node's icon from a set of 32x32
// pixel art (ui/node-icons.json), and the desk's touch icon, manifest
// icons and tab icon are drawn from the one it picked. The set's first
// icon is exe's own computer, the default; its four /ui/ files stay the
// embedded PNGs, byte for byte, so a node that picks nothing serves what
// it always has.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// nodeIcon is one icon of the set: 32 rows of 32 palette keys, '.' clear.
type nodeIcon struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Tile    string            `json:"tile"` // the Home Screen tile behind the art
	Palette map[string]string `json:"palette"`
	Rows    []string          `json:"rows"`

	px   [32][32]color.NRGBA
	tile color.NRGBA
}

// defaultNodeIcon is the set's first icon: exe's own computer.
const defaultNodeIcon = "exe"

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func parseHexColor(s string) (color.NRGBA, error) {
	if !hexColorRE.MatchString(s) {
		return color.NRGBA{}, fmt.Errorf("colour %q: want #rrggbb", s)
	}
	v, _ := strconv.ParseUint(s[1:], 16, 32)
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}, nil
}

// parseNodeIcons reads the set and checks every icon in it.
func parseNodeIcons(b []byte) ([]*nodeIcon, error) {
	var set struct {
		Icons []*nodeIcon `json:"icons"`
	}
	if err := json.Unmarshal(b, &set); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i, ic := range set.Icons {
		if ic.ID == "" || seen[ic.ID] || ic.ID != strings.ToLower(ic.ID) {
			return nil, fmt.Errorf("icon %d: id %q missing, repeated or not lowercase", i, ic.ID)
		}
		seen[ic.ID] = true
		if ic.Name == "" {
			return nil, fmt.Errorf("%s: no name", ic.ID)
		}
		var err error
		if ic.tile, err = parseHexColor(ic.Tile); err != nil {
			return nil, fmt.Errorf("%s: tile: %v", ic.ID, err)
		}
		pal := map[byte]color.NRGBA{}
		for k, v := range ic.Palette {
			if len(k) != 1 || k == "." {
				return nil, fmt.Errorf("%s: palette key %q", ic.ID, k)
			}
			if pal[k[0]], err = parseHexColor(v); err != nil {
				return nil, fmt.Errorf("%s: palette %s: %v", ic.ID, k, err)
			}
		}
		if len(ic.Rows) != 32 {
			return nil, fmt.Errorf("%s: %d rows, want 32", ic.ID, len(ic.Rows))
		}
		for y, row := range ic.Rows {
			if len(row) != 32 {
				return nil, fmt.Errorf("%s: row %d is %d long, want 32", ic.ID, y, len(row))
			}
			for x := 0; x < 32; x++ {
				if row[x] == '.' {
					continue
				}
				c, ok := pal[row[x]]
				if !ok {
					return nil, fmt.Errorf("%s: row %d col %d: %q is not in the palette", ic.ID, y, x, row[x])
				}
				ic.px[y][x] = c
			}
		}
	}
	if len(set.Icons) == 0 || set.Icons[0].ID != defaultNodeIcon {
		return nil, fmt.Errorf("the set must open with %q, the default", defaultNodeIcon)
	}
	return set.Icons, nil
}

// nodeIcons is the set the binary ships, in the picker's order.
var nodeIcons, nodeIconIDs = func() ([]*nodeIcon, map[string]*nodeIcon) {
	b, err := uiFS.ReadFile("ui/node-icons.json")
	if err != nil {
		panic(err)
	}
	set, err := parseNodeIcons(b)
	if err != nil {
		panic("ui/node-icons.json: " + err.Error())
	}
	ids := map[string]*nodeIcon{}
	for _, ic := range set {
		ids[ic.ID] = ic
	}
	return set, ids
}()

// nodeIconFor is the icon a configuration names, or the default when it
// names none, or one this binary does not have (a config.json written by a
// newer exe).
func nodeIconFor(id string) *nodeIcon {
	if ic := nodeIconIDs[id]; ic != nil {
		return ic
	}
	return nodeIconIDs[defaultNodeIcon]
}

// render draws the icon into a size x size picture, each grid pixel a
// scale x scale block, centred; onTile fills the picture with the tile
// first (a Home Screen icon has no transparent corners: iOS would fill
// them black, Android's mask needs the bleed).
func (ic *nodeIcon) render(size, scale int, onTile bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	if onTile {
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = ic.tile.R, ic.tile.G, ic.tile.B, 255
		}
	}
	off := (size - 32*scale) / 2
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			c := ic.px[y][x]
			if c.A == 0 {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetNRGBA(off+x*scale+dx, off+y*scale+dy, c)
				}
			}
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		panic(err) // a bytes.Buffer does not fail
	}
	return buf.Bytes()
}

// svg is the icon as the desk draws its own: one path of one-pixel-high
// runs per colour, crisp at every scale.
func (ic *nodeIcon) svg() []byte {
	runs := map[color.NRGBA]*strings.Builder{}
	var order []color.NRGBA
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; {
			c := ic.px[y][x]
			n := 1
			for x+n < 32 && ic.px[y][x+n] == c {
				n++
			}
			if c.A != 0 {
				b := runs[c]
				if b == nil {
					b = &strings.Builder{}
					runs[c] = b
					order = append(order, c)
				}
				fmt.Fprintf(b, "M%d %dh%dv1H%dz", x, y, n, x)
			}
			x += n
		}
	}
	var out strings.Builder
	out.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32" shape-rendering="crispEdges">`)
	for _, c := range order {
		fmt.Fprintf(&out, `<path fill="#%02x%02x%02x" d="%s"/>`, c.R, c.G, c.B, runs[c].String())
	}
	out.WriteString("</svg>")
	return []byte(out.String())
}

// nodeIconFile is how each of the desk's icon files is drawn. The four
// the desk has always had keep their names (the manifest, the push
// notifications and old Home Screen bookmarks name them); the touch icon is
// new, opaque on its tile, where iOS used to fill icon-192's clear corners
// with black.
var nodeIconFiles = map[string]struct {
	kind             string
	size, scale      int
	onTile, embedded bool
}{
	"icon.svg":              {kind: "image/svg+xml", embedded: true},
	"icon-192.png":          {kind: "image/png", size: 192, scale: 6, embedded: true},
	"icon-512.png":          {kind: "image/png", size: 512, scale: 16, embedded: true},
	"icon-maskable-512.png": {kind: "image/png", size: 512, scale: 9, onTile: true, embedded: true}, // 9x keeps a full 32-grid inside the mask's safe circle
	"apple-touch-icon.png":  {kind: "image/png", size: 180, scale: 4, onTile: true},
}

var nodeIconCache sync.Map // "id/file" → []byte

func nodeIconBytes(ic *nodeIcon, file string) []byte {
	key := ic.ID + "/" + file
	if b, ok := nodeIconCache.Load(key); ok {
		return b.([]byte)
	}
	f := nodeIconFiles[file]
	var b []byte
	switch {
	case ic.ID == defaultNodeIcon && f.embedded:
		b, _ = uiFS.ReadFile("ui/" + file)
	case f.kind == "image/svg+xml":
		b = ic.svg()
	default:
		b = ic.render(f.size, f.scale, f.onTile)
	}
	nodeIconCache.Store(key, b)
	return b
}

// manifestBase is the embedded manifest, read once.
var manifestBase = func() map[string]any {
	b, err := uiFS.ReadFile("ui/manifest.json")
	if err != nil {
		panic(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		panic("ui/manifest.json: " + err.Error())
	}
	return m
}()

// nodeManifest is the manifest for a node's icon: the same files, their
// addresses carrying the icon's id, so a browser that keeps an installed
// app's icons by address sees a new one when the node picks another.
func nodeManifest(ic *nodeIcon) []byte {
	if ic.ID == defaultNodeIcon {
		b, _ := uiFS.ReadFile("ui/manifest.json")
		return b
	}
	key := ic.ID + "/manifest.json"
	if b, ok := nodeIconCache.Load(key); ok {
		return b.([]byte)
	}
	m := map[string]any{}
	for k, v := range manifestBase {
		m[k] = v
	}
	var icons []any
	for _, v := range manifestBase["icons"].([]any) {
		e := map[string]any{}
		for k, x := range v.(map[string]any) {
			e[k] = x
		}
		e["src"] = fmt.Sprint(e["src"]) + "?icon=" + ic.ID
		icons = append(icons, e)
	}
	m["icons"] = icons
	b, _ := json.MarshalIndent(m, "", "  ")
	nodeIconCache.Store(key, b)
	return b
}

// handleUIFile serves /ui/: the icon files and the manifest as this
// node's icon draws them, everything else as embedded.
func (s *Server) handleUIFile(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/ui/")
	var b []byte
	var kind string
	if f, ok := nodeIconFiles[rel]; ok {
		b, kind = nodeIconBytes(nodeIconFor(s.Config().Icon), rel), f.kind
	} else if rel == "manifest.json" {
		b, kind = nodeManifest(nodeIconFor(s.Config().Icon)), "application/manifest+json"
	} else {
		uiStatic.ServeHTTP(w, r)
		return
	}
	sum := sha256.Sum256(b)
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:8])+`"`)
	http.ServeContent(w, r, rel, time.Time{}, bytes.NewReader(b))
}
