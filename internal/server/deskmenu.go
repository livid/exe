package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// The desktop menu — the NeXT-style menu the web UI pops up at the pointer
// on a right-click of the desktop — is a text file the user can edit from
// its own Customize… item. The daemon owns the format: PUT parses and
// validates the text and rejects a bad file with the offending line, so a
// desktop never has to render a broken menu; GET hands back both the text
// (for the editor) and the parsed tree (for the menu). The file rides the
// reserved System app-data store (like the Icon Editor's icons.json), so it
// persists in ~/.exe/appdata/System, syncs to joined nodes, and its changes
// reach every open desktop over the /v1/apps/events stream.
//
// Format, one item per line:
//
//	Label<tab or 2+ spaces>action [argument…]   an item
//	Label                                      a submenu: indent the lines under it
//	-                                          a separator
//	# …                                        a comment
//	@vms / @apps / @windows                    a self-filling list spliced in place;
//	Label<sep>@vms                             the same list as a named submenu
const deskMenuFile = "menu.txt"

// deskMenuMax caps the menu text; the factory file is about 1.5 KB.
const deskMenuMax = 64 << 10

const (
	deskMenuMaxDepth = 4   // root plus three levels of submenus
	deskMenuMaxItems = 300 // items and separators, all levels
	deskMenuMaxLabel = 60  // runes
)

// deskMenuItem is one node of the parsed menu. Exactly one shape applies:
// an action item (Action set), a submenu (Items set), a generator (Gen set,
// Label optional — labelled it is a submenu filled by the desktop, bare it
// splices its entries in place), or a separator (Sep).
type deskMenuItem struct {
	Label  string         `json:"label,omitempty"`
	Action string         `json:"action,omitempty"`
	Args   []string       `json:"args,omitempty"`
	Items  []deskMenuItem `json:"items,omitempty"`
	Gen    string         `json:"gen,omitempty"`
	Sep    bool           `json:"sep,omitempty"`
}

// deskMenuAction describes what an action accepts. The desktop maps the
// name to the same function its menu bar or icons run; the daemon only
// checks the shape so the editor can point at the exact bad line.
type deskMenuAction struct {
	minArgs, maxArgs int
	usage            string // the argument hint in errors, e.g. "<name> [tab]"
	raw              bool   // the whole remainder is one argument (a command line)
	check            func(args []string) error
}

var deskMenuTabs = map[string]bool{"svc": true, "term": true, "vibe": true, "expose": true, "sess": true, "notes": true}

var deskMenuActions = map[string]deskMenuAction{
	// menu bar equivalents
	"about": {}, "newvm": {}, "upload": {}, "closewin": {}, "refresh": {},
	"winvms": {}, "winmyapps": {}, "winchat": {}, "winnews": {}, "winicons": {},
	"winconfig": {}, "winlog": {}, "join": {}, "cfstatus": {}, "cfwizard": {},
	"token": {}, "docs": {}, "skillguide": {},
	// desktop icons and windows
	"claude": {}, "codex": {}, "search": {}, "trash": {}, "customize": {},
	// a bare terminal is a shell; with a command line it's a shortcut to a
	// CLI tool, run in a login shell and gone when the tool exits
	"terminal":  {maxArgs: 1, raw: true, usage: "[command]"},
	"workspace": {maxArgs: 1, usage: "[folder]", check: checkMenuPath},
	"edit":      {minArgs: 1, maxArgs: 1, usage: "<file>", check: checkMenuPath},
	"app":       {minArgs: 1, maxArgs: 1, usage: "<name>"},
	"chat":      {maxArgs: 1, usage: "[vm]"},
	"vm": {minArgs: 1, maxArgs: 2, usage: "<name> [svc|term|vibe|expose|sess|notes]", check: func(args []string) error {
		if len(args) == 2 && !deskMenuTabs[args[1]] {
			return fmt.Errorf("%q is not a VM tab (svc, term, vibe, expose, sess or notes)", args[1])
		}
		return nil
	}},
	"url": {minArgs: 1, maxArgs: 1, usage: "<https://…>", check: func(args []string) error {
		if !strings.HasPrefix(args[0], "http://") && !strings.HasPrefix(args[0], "https://") {
			return errors.New("url needs an http:// or https:// address")
		}
		return nil
	}},
}

var deskMenuGens = map[string]bool{"vms": true, "apps": true, "windows": true}

func checkMenuPath(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if _, err := scopedPath("/", args[0]); err != nil {
		return fmt.Errorf("%q is not a Workspace path", args[0])
	}
	return nil
}

// deskMenuDefault is the factory menu. Its header doubles as the format's
// documentation — Customize… opens exactly this text the first time.
const deskMenuDefault = `# Desktop menu — right-click the desktop (long-press on a phone) to open it.
#
# One item per line: a label, then a tab or two spaces, then an action.
# A label alone on a line starts a submenu — indent the lines under it.
# "-" alone is a separator, "#" starts a comment, blank lines are skipped.
# Save checks the file and applies the menu to every desk sharing this one;
# a bad line is reported and nothing changes. An empty file restores this.
#
# Actions:
#   newvm  upload  refresh  closewin  search  trash  customize
#   terminal [command]  claude  codex  workspace [folder]  edit <file>
#   winvms  winmyapps  winchat  winnews  winicons  winconfig  winlog
#   vm <name> [svc|term|vibe|expose|sess|notes]  app <name>  chat [vm]
#   join  cfstatus  cfwizard  token  url <https://…>
#   about  docs  skillguide
# Lists that fill themselves in: @vms  @apps  @windows
#   alone on a line they expand in place; after a label they fill a submenu.
# A shortcut to a CLI tool: "btop  terminal btop" opens it in a Terminal window.

New VM…              newvm
New Terminal         terminal
Workspace            workspace
-
Virtual Machines     @vms
Apps                 @apps
Windows              @windows
-
Tools
  Chat               winchat
  Claude Code        claude
  Codex              codex
  Newsfeed           winnews
  Search…            search
  Icon Editor        winicons
  Daemon Log         winlog
  Configuration      winconfig
  Trash              trash
Cloudflare
  Status…            cfstatus
  Setup Wizard…      cfwizard
Help
  exe Documentation…   docs
  Agent Skill Guide…   skillguide
  About This Computer  about
-
Customize…           customize
`

// the label/action separator: a tab, or two or more spaces
var deskMenuSepRE = regexp.MustCompile(`\t| {2,}`)

// menuNode is the parser's mutable tree; pointers keep children stable
// while siblings are still being appended.
type menuNode struct {
	line     int
	item     deskMenuItem
	children []*menuNode
	header   bool // a bare label: needs children by the end
}

func (n *menuNode) toItem() deskMenuItem {
	it := n.item
	if n.header {
		it.Items = make([]deskMenuItem, 0, len(n.children))
		for _, c := range n.children {
			it.Items = append(it.Items, c.toItem())
		}
	}
	return it
}

// parseDeskMenu turns the text into a tree, or reports the first problem
// with its line number ("line 7: …") so the editor can point at it.
func parseDeskMenu(text string) ([]deskMenuItem, error) {
	if len(text) > deskMenuMax {
		return nil, errors.New("menu text is too long")
	}
	type level struct {
		indent string
		nodes  *[]*menuNode
	}
	root := &menuNode{header: true}
	stack := []level{{"", &root.children}}
	var last *menuNode // most recent item at the current level
	count := 0
	fail := func(ln int, format string, a ...any) ([]deskMenuItem, error) {
		return nil, fmt.Errorf("line %d: %s", ln, fmt.Sprintf(format, a...))
	}
	for i, raw := range strings.Split(text, "\n") {
		ln := i + 1
		line := strings.TrimRight(raw, " \t\r")
		body := strings.TrimLeft(line, " \t")
		if body == "" || strings.HasPrefix(body, "#") {
			continue
		}
		indent := line[:len(line)-len(body)]
		top := stack[len(stack)-1]
		switch {
		case indent == top.indent:
		case len(indent) > len(top.indent) && strings.HasPrefix(indent, top.indent):
			// deeper: the line belongs under the previous item
			if last == nil {
				return fail(ln, "indented, but there is no item above to belong to")
			}
			if !last.header {
				return fail(ln, "%q already has an action, so nothing can be indented under it", last.item.Label)
			}
			if len(stack) >= deskMenuMaxDepth {
				return fail(ln, "submenus nest too deep (%d levels at most)", deskMenuMaxDepth)
			}
			stack = append(stack, level{indent, &last.children})
			top = stack[len(stack)-1]
		default:
			// shallower: back out to the level with this exact indentation
			for len(stack) > 1 && stack[len(stack)-1].indent != indent {
				stack = stack[:len(stack)-1]
			}
			top = stack[len(stack)-1]
			if top.indent != indent {
				return fail(ln, "indentation doesn't line up with any line above")
			}
		}
		if count++; count > deskMenuMaxItems {
			return fail(ln, "too many items (%d at most)", deskMenuMaxItems)
		}
		n := &menuNode{line: ln}
		switch {
		case strings.Trim(body, "-") == "":
			n.item.Sep = true
		case strings.HasPrefix(body, "@"):
			f := strings.Fields(body)
			if !deskMenuGens[f[0][1:]] {
				return fail(ln, "unknown list %s (use @vms, @apps or @windows)", f[0])
			}
			if len(f) > 1 {
				return fail(ln, "%s takes nothing after it", f[0])
			}
			n.item.Gen = f[0][1:]
		default:
			label, action := body, ""
			if loc := deskMenuSepRE.FindStringIndex(body); loc != nil {
				label, action = body[:loc[0]], strings.TrimSpace(body[loc[1]:])
			}
			if utf8.RuneCountInString(label) > deskMenuMaxLabel {
				return fail(ln, "label is too long (%d characters at most)", deskMenuMaxLabel)
			}
			n.item.Label = label
			if action == "" {
				n.header = true
				break
			}
			f := strings.Fields(action)
			if strings.HasPrefix(f[0], "@") {
				if !deskMenuGens[f[0][1:]] {
					return fail(ln, "unknown list %s (use @vms, @apps or @windows)", f[0])
				}
				if len(f) > 1 {
					return fail(ln, "%s takes nothing after it", f[0])
				}
				n.item.Gen = f[0][1:]
				break
			}
			spec, ok := deskMenuActions[f[0]]
			if !ok {
				return fail(ln, "unknown action %q", f[0])
			}
			args := f[1:]
			if spec.raw {
				args = nil
				if rest := strings.TrimSpace(action[len(f[0]):]); rest != "" {
					args = []string{rest}
				}
			}
			switch {
			case len(args) < spec.minArgs:
				return fail(ln, "%s needs %s", f[0], spec.usage)
			case len(args) > spec.maxArgs && spec.maxArgs == 0:
				return fail(ln, "%s takes no argument", f[0])
			case len(args) > spec.maxArgs:
				return fail(ln, "%s takes only %s", f[0], spec.usage)
			}
			if spec.check != nil {
				if err := spec.check(args); err != nil {
					return fail(ln, "%s", err)
				}
			}
			n.item.Action = f[0]
			if len(args) > 0 {
				n.item.Args = args
			}
		}
		*top.nodes = append(*top.nodes, n)
		last = n
	}
	// a bare label is only a submenu once something is indented under it
	var check func(nodes []*menuNode) error
	check = func(nodes []*menuNode) error {
		for _, n := range nodes {
			if n.header && len(n.children) == 0 {
				return fmt.Errorf("line %d: %q has no action and nothing indented under it (a label and its action are separated by a tab or two spaces)", n.line, n.item.Label)
			}
			if err := check(n.children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(root.children); err != nil {
		return nil, err
	}
	return root.toItem().Items, nil
}

func (s *Server) deskMenuPath() string {
	return filepath.Join(s.appDataDir("System"), deskMenuFile)
}

// deskMenuResponse is what GET and PUT return: the text for the editor,
// the parsed tree for the menu, whether that is the factory menu, and — for
// a stored file that no longer parses (synced in from elsewhere, say) — the
// problem, with the factory tree standing in so the desktop keeps a menu.
func (s *Server) deskMenuResponse() map[string]any {
	res := map[string]any{"default": true}
	text := deskMenuDefault
	if b, err := os.ReadFile(s.deskMenuPath()); err == nil {
		text = string(b)
		res["default"] = false
		if st, err := os.Stat(s.deskMenuPath()); err == nil {
			res["modified"] = st.ModTime().UTC().Format(time.RFC3339)
		}
	}
	res["text"] = text
	menu, err := parseDeskMenu(text)
	if err != nil {
		res["error"] = err.Error()
		menu, _ = parseDeskMenu(deskMenuDefault)
	}
	res["menu"] = menu
	return res
}

func (s *Server) handleDeskMenuGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.deskMenuResponse())
}

// handleDeskMenuPut takes the raw text. It must parse, or nothing changes
// and the error names the line. Blank text removes the custom file and
// brings the factory menu back. The write goes through the same lock and
// peer hooks as an app-data PUT, so joined nodes and other open desktops
// pick the new menu up the same way.
func (s *Server) handleDeskMenuPut(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(io.LimitReader(r.Body, deskMenuMax+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(b) > deskMenuMax {
		writeErr(w, http.StatusRequestEntityTooLarge, errors.New("menu text exceeds 64 KB"))
		return
	}
	text := string(b)
	if !utf8.ValidString(text) {
		writeErr(w, http.StatusBadRequest, errors.New("menu text is not UTF-8"))
		return
	}
	reset := strings.TrimSpace(text) == ""
	if !reset {
		if _, err := parseDeskMenu(text); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	p := s.deskMenuPath()
	var werr error
	changed := false
	s.withFileLock(func() {
		if reset {
			if err := os.Remove(p); err == nil {
				changed = true
				if s.Peers != nil {
					s.Peers.LocalDelete("System", deskMenuFile)
				}
			} else if !os.IsNotExist(err) {
				werr = err
			}
			return
		}
		if werr = writeFileAtomic(p, b); werr == nil {
			changed = true
			if s.Peers != nil {
				s.Peers.LocalWrite("System", deskMenuFile)
			}
		}
	})
	if werr != nil {
		writeErr(w, http.StatusInternalServerError, werr)
		return
	}
	if changed {
		s.BroadcastAppData("System", deskMenuFile, reset, r.Header.Get("X-Exe-Client"))
	}
	writeJSON(w, http.StatusOK, s.deskMenuResponse())
}

// writeFileAtomic lands body at p via a temp file in the same directory and
// a rename, so a reader never sees a half-written menu.
func writeFileAtomic(p string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return err
	}
	if _, err = tmp.Write(body); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
