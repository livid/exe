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
- The repo is public: no real email address in code, test fixtures (swap
  the ones in CLI or API output you copy in), docs, commit messages, hub
  posts or screenshots. Use a reserved example (`someone@example.com`).
  `.githooks` refuses a commit whose added lines or message hold one;
  enable it once per checkout with `git config core.hooksPath .githooks`,
  and skip it (`--no-verify`) only with Livid's say-so. The daemon's logs
  mask addresses, but a screenshot of an account, settings or log view is
  looked over for one first, or taken on a scratch daemon.
- For publicly shareable build artifacts under 20 MB, pin them in Kubo and
  record the CID, a working download URL and SHA-256 in the relevant doc.
  Verify the pin and a fresh download; keep the binaries out of Git.
  Releases are the exception: they live on GitHub only.
- `NOTES.md` is what the next release tells its users changed, and
  Software Update shows it. A change a user of a release will notice gets
  its line there in the commit that ships it: what they get and where it
  is, not the commit subject. At most eight short items — when a ninth
  turns up, the one a user would miss least goes. `deploy/release.sh
  notes` checks it; the rules are in `docs/release.md`, "The notes".
  `CHANGELOG.md` keeps what every release said, the site shows it at
  /docs/changelog, and only `publish` adds a release to it.
- Releases (Linux and macOS, x86-64 and ARM64 each, and Windows x86-64;
  `docs/release.md`): build with `deploy/release.sh build` — the macOS
  binaries are built and signed on the Mac named in `deploy/release.env` —
  test on `lab`, `precision`, the Mac `birdie` and the PC `world` through
  `deploy/release-mirror.py`, and
  publish with `deploy/release.sh publish` only when Livid says "release a
  new version": it pushes `main` and a tag, and a published release cannot
  be changed. The version is the UTC date. The hub carries the
  announcement, not the files. Never run a released build's `exe setup` on
  spark.
- Build: `export PATH=$PATH:/usr/local/go/bin && make build` (Go is not on
  the tool shell's PATH). Restart: `XDG_RUNTIME_DIR=/run/user/1000
  systemctl --user restart exe` — no sudo; VMs return through
  `~/.exe/autostart`, the agent tmux servers survive. The desktop and the
  sysapps ship inside the binary, so their changes need build + restart;
  exe-apps do not. Rebuild and restart once a feature is done.
- The homepage, https://exe.v2core.com, is
  `internal/server/site/index.html`: one static page in the hub public
  pages' Platinum blocks, served out of the binary (`internal/server/site.go`,
  proxy backend `exe:site`, published once with `exe site`). It ships with
  the daemon, so a change to it needs build + restart like the desktop, and
  its screenshot is the README's too. Its readers are counted by
  `github.com/livid/exe-stats` (the hub's /stats, now a package of its
  own — /www/exe-stats, `replace`d locally until it is published) into
  `~/.exe/stats.db`, and read back at exe.v2core.com/stats.
- Test: `go test ./...`. Check UI in headless Chromium
  (`~/tools/playwright`, node at `~/.nvm/versions/node/v24.15.0/bin`);
  screenshot and look at every UI change at device pixel ratios 1, 1.5
  (Livid uses Windows at 150 percent) and 2. A desk page loaded against
  the live daemon reopens Livid's unattached Terminal sessions (real
  shells in tmux), and a new Terminal starts one: a test that opens or
  closes Terminal windows routes `/v1/host/terminals` to a stub.
- The data in `~/.exe` has no backup, and agent shells export
  `EXE_HOME=~/.exe`: never derive a test's scratch path from `EXE_HOME`
  or `HOME` — give it a variable of its own. A test that writes a
  daemon's files runs a scratch daemon (its own home in the scratchpad,
  port 7797), refuses paths under `~/.exe` and port 7777, and first
  checks that the daemon it drives serves the file it wrote. A test
  against the live daemon stubs every PUT and DELETE, not only
  `/v1/host/terminals`.
- Two agents share this working tree. Run `git status` before editing,
  leave the other agent's uncommitted files alone, and say on the hub
  what you are about to commit and when you restart the daemon.
- Projects, the issue tracker on the exe desk, holds this repo as the
  project `EXE` (`/www/exe-projects`; its README says how it is kept).
  An issue id such as `EXE-12` or `PROJ-5` is read whole with
  `/www/exe-projects/exe-projects show PROJ-5` (the binary is not on
  PATH; `help` lists the rest), and in this checkout a dot is its
  project: `exe-projects ls .`, `exe-projects new . "Title" -m "…"`.
  Write under your own name: `EXE_PROJECTS_KEY` names your ed25519 key
  — Claude's is `~/.claude/hub/claude_ed25519.pem`, Codex's its hub key
  `~/.codex/hub/codex_ed25519.pem` — and `exe-projects whoami -name "…"`,
  once, says what to call you. Without a key a record is signed as the
  node, which is Livid.
- An issue about something seen on screen carries a screenshot of it,
  attached when it is filed: `-a shot.png` on `exe-projects new`, or on
  `exe-projects comment` for one added later. The relevant window only,
  never the desktop, and nothing private: an attachment is in IPFS,
  where anyone who learns its address can fetch it. Playwright's
  headless Chromium draws a scroll bar only with
  `ignoreDefaultArgs: ["--hide-scrollbars"]`.
- When something notable is finished, post it to the hub in the first
  person, short, with a screenshot of the relevant window only — never
  the whole desktop.

## UI rules (details and numbers in docs/platinum.md)

- Copy the shared Platinum blocks verbatim — `button.ghost`, the sunken
  field, the 15px status bar, the grow box (a fixed-size window needs no
  grow box: `"grow": false`), the scrollbar
  (exe-apps Tides/Notes). The `.popup` menu button is not copied: it is
  the one block in exe-stats (`popup.css`), linked as
  `/platinum/popup.css` by apps and sysapps and embedded by the hub as
  `{{popup}}`, like the chrome. Buttons are
  20px, OK/Cancel 58px, 12px apart and from edges; only the Return
  default wears the ring. 12px Charcoal type, `cursor: default`, no hover
  states, no pointing hand.
- Every 1px line is a CSS border on a box of its own; never a gradient.
  Glyphs are crispEdges SVG. Pseudo-elements need `box-sizing` said.
- One seam, one line: where two chrome edges meet there is exactly one
  1px dark line — drop the other border.
- No buttons on a status line; they get a row of their own.
- Desktop text goes through `L()` and has its line in every catalog in
  `internal/server/ui/i18n/` (Simplified Chinese, Japanese), in the same
  commit; `go test ./internal/server` names a missing one. Code never
  compares displayed text (docs/platinum.md, "Palette and type").
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

- Built-in app folders and IDs use lowercase names without spaces, such as
  `macos9`, `hub`, and `bluepencil`. Keep display names in `app.json`'s
  `title`; do not use a display title as a new source directory or app ID.

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
- An emulated Mac's arrow is where the browser's pointer is, in the
  Installer and after: never more than a pixel off. This is very
  important to Livid. Mac OS 9 has it by the tablet driver in its ROM;
  Mac OS X by its own driver, on a board whose only USB pointer is the
  tablet (`internal/macos9/system.go`: beside a USB mouse the tablet is
  not the pointer QEMU feeds). A guest's system update can change it:
  Tiger at 10.4.11 leaves 15% of the tablet's range unused where 10.4.0
  used all of it, and the viewer corrects for what it sees the arrow do
  (`trims` there, `pointerTrim` in the Mac apps' `app.js`). Check it
  through the browser, never over QMP alone, whose absolute events reach
  the tablet whichever pointer the display is feeding: `query-mice` names
  the tablet as current, and `~/tools/playwright/exe-macosx-pointer-test.js`
  passes on a scratch daemon, on the installer boot, on an installed
  system and after the guest's system was updated. A window that does not
  settle fails it too (`exe-mac-settle-test.js`).

## Docs to keep current

`internal/server/docs.md` is the in-app "Using exe" text — update it when
a feature is user-visible. `docs/platinum.md` is the UI guide.
`/www/exe-apps/CLAUDE.md` holds the app conventions. `/www/exe-hub/PLAN.md`
is the hub's plan.
