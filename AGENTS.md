# Working in exe

For any coding agent in this checkout — Claude and Codex both read this
file (CLAUDE.md is a symlink to it). exe is Livid's personal VM cloud: a Go
daemon (`cmd/exe`, `internal/`) whose web UI is a Mac OS 9 Platinum desktop
in one file, `internal/server/ui/index.html`. System apps live in
`internal/server/sysapps/` (embedded in the binary); user apps in
`/www/exe-apps` (served live from disk, see its CLAUDE.md); City in
`/www/exe-city`; the hub in `/www/exe-hub` (its PLAN.md is its source of
truth). The UI guide is `docs/platinum.md` — read it before touching UI.

## Workflow

- Commit on `main`. No branches, no PRs. First line `Area: what changed`
  (Desktop:, Daemon:, Hub:, Docs:), body says why. Never commit `output/`
  or other scratch.
- Build: `export PATH=$PATH:/usr/local/go/bin && make build` (Go is not on
  the tool shell's PATH). Restart: `XDG_RUNTIME_DIR=/run/user/1000
  systemctl --user restart exe` — no sudo; VMs return through
  `~/.exe/autostart`, the agent tmux servers survive. The desktop and the
  sysapps ship inside the binary, so their changes need build + restart;
  exe-apps do not. Rebuild and restart once a feature is done.
- Test: `go test ./...`. Check UI in headless Chromium
  (`~/tools/playwright`, node at `~/.nvm/versions/node/v24.15.0/bin`);
  screenshot and look at every UI change at device pixel ratios 1, 1.5
  (Livid uses Windows at 150 percent) and 2.
- Two agents share this working tree. Run `git status` before editing,
  leave the other agent's uncommitted files alone, and say on the hub
  what you are about to commit and when you restart the daemon.
- When something notable is finished, post it to the hub in the first
  person, short, with a screenshot of the relevant window only — never
  the whole desktop.

## UI rules (details and numbers in docs/platinum.md)

- Copy the shared Platinum blocks verbatim — `button.ghost`, the sunken
  field, the 15px status bar, the grow box (a fixed-size window needs no
  grow box: `"grow": false`), the scrollbar
  (exe-apps Tides/Notes), the `.popup` menu button (sysapps/BluePencil). Buttons are
  20px, OK/Cancel 58px, 12px apart and from edges; only the Return
  default wears the ring. 12px Charcoal type, `cursor: default`, no hover
  states, no pointing hand.
- Every 1px line is a CSS border on a box of its own; never a gradient.
  Glyphs are crispEdges SVG. Pseudo-elements need `box-sizing` said.
- One seam, one line: where two chrome edges meet there is exactly one
  1px dark line — drop the other border.
- No buttons on a status line; they get a row of their own.
- No layout jumps: content that arrives later fills placeholders that
  already have its final shape.
- Icons are 32-grid pixel art in the palette standard (index.html, above
  `MINI_CHAT_ICON`): black outlines, no baked shadow, an object not a
  logo. Every new system icon registers in `ICON_DEFS` so the Icon Editor
  can repaint it; state-driven art keeps a reserved colour the code
  repaints.
- A phone shows one fullscreen window under the 20px bar, keeps the safe
  areas clear, and shrinks a button row to glyphs when words will not fit.
- For pixel truth: the OS 8 HIG mirror at dev.os9.ca, and a real Mac OS 9
  (the local QEMU one in `output/mac-os9`, screendump over its QMP socket;
  or macos9.app). Sample, then diff.

## Data and behaviour

- App state goes through `/v1/apps/<Name>/data/<file>` under the sync
  contract in `/www/exe-apps/CLAUDE.md`; a new record-bearing file also
  needs its merge schema in `internal/peer/merge.go`.
- The desk right-click menu is user-customised: factory-menu edits will
  not show for Livid; tell them the line to add.
- The Newsfeed is for node events, not echoes of hub posts or replies.
- WriteFile stages in /tmp and copies in place on purpose (`cat >`
  semantics); do not reintroduce a rename.
- Model calls that face the user run with maximum thinking; never force
  thinking off.

## Docs to keep current

`internal/server/docs.md` is the in-app "Using exe" text — update it when
a feature is user-visible. `docs/platinum.md` is the UI guide.
`/www/exe-apps/CLAUDE.md` holds the app conventions. `/www/exe-hub/PLAN.md`
is the hub's plan.
