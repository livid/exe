# Building exe's Platinum UI

exe's web UI is Mac OS 9's Platinum look, and so is every app that opens in
it. This is the shared knowledge: where the truth comes from, the numbers,
the colours, the blocks to copy, and the rules Livid has asked for by name.
The reference copies live in code — copy them verbatim rather than
restyling: `internal/server/ui/index.html` (the desktop),
`/www/exe-apps/Tides/index.html` and `Notes/index.html` (apps),
`internal/server/sysapps/BluePencil/index.html` (the pop-up menu button).

## Where the truth comes from

- The Mac OS 8 Human Interface Guidelines, mirrored at
  https://dev.os9.ca/techpubs/mac/HIGOS8Guide/ (chapters are `thig-N.html`,
  figures under `graphics/`). This is the authority. Download a figure and
  pixel-sample it (PIL) for exact colours and spacing; do not approximate.
  thig-12 default buttons (fig. 2-3 `HIG_CG-105.gif`), thig-14 pop-up menu
  buttons (fig. 2-7 `HIG_CG-081.gif`), thig-51 dialog spacing, thig-52
  control layout, fig. 3-4 `HIG_CG-070.gif` the movable alert.
- What the HIG does not picture, a real Mac OS 9 does: macos9.app (Infinite
  Mac) boots OS 9 in the browser. Drive it headless, read the canvas with
  `toDataURL` (never `getContext`, it blanks the emulator), dump pixels as a
  text map. `~/tools/playwright/ref/os9-control-strip/mac9d.js` is the
  worked example; the Control Strip was rebuilt from it pixel for pixel.
- macthemes.garden's Platinum values are where the window chrome came from.

## Pixels at every scale

- Livid tests on Windows at 150 percent (device pixel ratio 1.5). Verify at
  1, 1.25, 1.5 and 2 with an 8x nearest-neighbour zoom and look.
- Every 1px line is a CSS border on a box of its own (an element or a
  pseudo-element). Borders paint as exactly one device pixel at any scale;
  background gradients and sprite rows come out 1 or 2 pixels tall by
  turns, and small triangles grow tails at the apex.
- Where two bevels of different colours meet at a corner, the browser
  splits the corner pixel; when the original leaves that pixel as face
  colour, give each L its own box (the Control Strip's wells and tiles).
- Pseudo-elements are content-box: the global `* { box-sizing: border-box }`
  does not reach `::before` and `::after`. Say it explicitly.
- Only glyphs are art: SVG with `shape-rendering: crispEdges`, and under
  `@media (min-resolution: 1.01dppx) and (max-resolution: 1.99dppx)` a
  `geometricPrecision` copy so the apex stays a clean point.

## Palette and type

- Root values, the same in the desktop and every app:
  `--black #262626; --g200 #eee; --g300 #ddd; --g400 #ccc; --g500 #bbb;
  --g600 #999; --g700 #808080; --g800 #666; --hl #333399; --lav #ccf`.
  Highlight is `--hl` with white text. The desktop is a lavender pattern
  on `#ccf`.
- Type is `12px/1.45 "Charcoal", "Chicago", "Geneva", "Lucida Grande",
  "Lucida Sans", Geneva, Verdana, sans-serif`. Bold for menu titles, window
  titles and buttons. 11px for status lines and icon labels, 10px for
  secondary meta. Never 13px, never a system font stack.
- Chrome is `user-select: none; cursor: default`. OS 9 had no pointing
  hand and no hover states: a control changes only while pressed (`:active`),
  menus alone highlight under the pointer.

## Controls, with the HIG's numbers

- Push button (`button.ghost`): 20px high, `#ddd` face, 1px `#262626`
  border, 3px radius, bold 12px, padding `2px 13px`. Bevel: `inset 1px 1px
  0 #ddd, inset -1px -1px 0 #777, inset 2px 2px 0 #fff, inset -2px -2px 0
  #aaa`. Pressed: `#666` face, white text, bevel `#444 / #888 / #555 /
  #777`. Disabled: `#777` text, `#888` border, no bevel. Notes adds `.danger`
  (`#7a1010` text), `.armed` (red, the two-click delete), `.sm`; Tides adds
  `.on` (a depressed segment, `#888`).
- Default button: only the one Return triggers wears the ring, `outline: 2px
  solid #262626; outline-offset: 1px; border-radius: 4px` (the HIG's 2px
  black ring 3px out around a bevelled gap).
- Layout (dialog guidelines): buttons 20px high; OK and Cancel 58px wide, one
  width per set; 12px between buttons and 12px from any window edge; the set
  at the lower right, default rightmost, Cancel to its left; 4 to 6px between
  items in a group, 16px between groups; a checkbox is a 12px box with 5px to
  its label; pop-ups are 20px high with 6px between stacked ones.
- Text field: white, 1px `#262626`, no radius, 20px, padding `2px 5px`, the
  sunken frame as shadows `-1px -1px 0 #999` (top and left) and `1px 1px 0
  #fff` (bottom and right); focus is `outline: 2px solid #9999fe` at offset 0.
- Pop-up menu button: Blue Pencil's `.popup` block, from HIG fig. 2-7: 19px
  rounded black outline, white top and left, `#aaa` bottom, a 21px arrow
  well on the right with its own bevel and two 7px triangles, and a
  transparent native `<select>` inside. Never a bare select.
- Status bar: 15px total, a 1px black top border and a 14px `#ddd` face,
  11px `#333` text, `inset 1px 1px 0 rgba(255,255,255,.6), inset -1px -1px
  0 #aaa`. Text only. Buttons never sit on a status line; they get a row of
  their own in the content area, laid out as above.
- Grow box: the 15px SVG sampled from OS 9, at the window's bottom right;
  its black top row lands on the status bar's line. An app streams
  `{exe:"grow", dx, dy}` through the bridge and the desktop resizes. A
  fixed-size window needs no grow box: if the window cannot be resized
  (a dialog, an About box, a guest display shown at whole-number scales
  like the Mac OS 9 app) it has no tile, and an app says `"grow": false`
  in app.json so the desktop adds no edge grips either. On a phone every
  app hides its grow box; the window fills the screen there.
- Scrollbars: the pixel-sampled 15px block (track `#777 #888 #aaa #bbb
  #ccc`, thumb `#ccccff #9999ff #6666cc` with the ridged grip, 16px buttons
  with 8x4 arrows, only the trailing pair, the `scrolled-y` and `at-y-end`
  end merges). Copy it whole. Against a frame the bar has no trailing
  border; the frame's line is the bar's edge.

## Chrome the desktop draws

- Window: `#ccc` body, 1px `#262626` border, hard shadow `2px 2px 0`, inset
  white and `#999` bevel. Title bar: 13px boxes (close, zoom, shade) with a
  2px inner black ring, stripes of 2px rows `#fff / #777` on `#ddd`, a bold
  12px title on a `#ccc` field. An alert has a red-striped bar (`#fcc`
  field, `#fff / #f66` rows, `#f99` bevels), no title and no close box, and
  holds only an icon, a bold label beside it, a plain narrative and buttons.
- Menu bar: 20px `#ddd`, inset white top and left, `#999` bottom and right,
  1px black bottom line; bold titles with 10px side padding; an open title
  inverts to `#333399`. Menus: `#eee`, 1px black, hard shadow `2px 2px 0
  rgba(38,38,38,.85)`, items `3px 22px 3px 18px`, hover inverted, disabled
  `#888`, separators a 1px `#999` line over a 1px white one. Contextual menus
  are `#ddd`.
- Desktop icons: 32x32 pixel art at exactly 32 CSS px, 11px white labels
  with a 1px black text shadow; selected is a `#333399` label and a darkened
  icon. Window lists show the 32px art scaled to 16.

## Seams, stability, phones

- Every seam where two pieces of chrome meet shows exactly one 1px dark
  line. When both sides bring a border, drop one. The stack is black line,
  then white highlight, then face.
- Nothing loaded later may move what is already on screen: paint the final
  structure as placeholders and fill it in place.
- A phone runs one fullscreen window under the 20px bar, keeps the safe
  areas and the home indicator clear, and shrinks a button row to glyphs
  when words will not fit (the Hub's Find, Refresh, Profile).

## Icons

- The palette standard is in index.html above `MINI_CHAT_ICON`: grays `#000
  #444 #777 #888 #aaa #ccc #eee #fff`; the platinum beige ramp `#55524b
  #6e6a61 #8b867b #b7b2a7 #c8c3b8 #dad5ca #edeae2`; the folder blue-violet
  ramp `#222244` to `#ececfd`; one accent hue per meaning (VM screen-green
  `#7fd67f` is reserved). Outlines pure black, highlights pure white,
  shadows are black at 0.2 to 0.5 opacity, never gray pixels; chromatic
  colours as 6-digit hex.
- An icon is a 32x32 `crispEdges` SVG of rects and paths on a one-pixel
  grid, rendered at exactly 32 CSS px; the window lists show the same art
  halved to 16 (`sizeSvg`), and hand-drawn 16px minis are not wanted (tried
  and reverted; the few that remain serve search results and About). The
  icon carries no shadow of its own: the desktop adds the drop shadow with
  a filter and darkens the art when selected. An icon is an object you can
  name, drawn the way OS 9 drew its own (a machine, a document, a tool);
  a logo is not an icon, and a 16px glyph scaled to 2x2 blocks is the
  wrong grid.
- The Icon Editor (Windows → Icon Editor) is a hand-maintained registry,
  `ICON_DEFS` in index.html, not discovery. An entry has `key`, `label`,
  `size` and either `dom` (a selector, when the art lives in one place in
  the page) or `get` + `set` (when it lives in a `let` variable that every
  draw site rereads, or repaints through `applyIconOverrides`); `disp`
  re-stamps art displayed smaller than its grid (the Apple menu's 15px).
  Factory art is captured at load into `ICON_FACTORY`; a repaint is saved
  to the System app data as `icons.json` (`{icons: overrides, custom:
  user-made}`), which reaches every desktop on the node and every joined
  node like any app data, and the gallery offers Edit, Copy SVG (the
  run-length form the editor writes) and Revert to Factory. The editor
  rasterises an SVG one to one onto its grid, so it faithfully keeps
  whatever grid it is given.
- Art that changes with state keeps a reserved colour the code repaints
  at draw time: the VM screen-green, the Cloudflare lamp's two greens; the
  phone clock draws its hands live over registered face art.
- User app bundles (exe-apps) stay outside the editor by design: their
  `icon.svg` is drawn as an image. A system app embedded in the exe binary
  (`internal/server/sysapps/*`) is system UI and its icon belongs in the
  editor; today none registers (Hub, Blue Pencil, Mac OS 9), and the
  generic fix is for the desktop to draw a sysapp's desktop icon from a
  registry entry keyed `app-<name>` (get/set over the bundle's icon.svg
  text) instead of an `<img>`. Until then, at least draw the art to the
  rules above.

## Apps

- One `index.html`, vanilla JS, inline CSS, no frameworks or CDNs; the
  bundle folder is the app's identity; `app.json` sizes the window.
- The desktop bridge: `{exe:"focus"}` on pointerdown, `grow-start / grow /
  grow-end`, `hide` and `show` (pause loops and timers), `data-changed`.
- State goes through `/v1/apps/<Name>/data/<file>` under the sync contract
  in `/www/exe-apps/CLAUDE.md`: debounced serialized whole-document PUT
  with `X-Exe-Seq`, keepalive flush on pagehide, a loaded guard, ids and
  updated stamps and tombstones on records.
- Check every change in headless Chromium at 1x, 1.5x and 2x, and look at
  the screenshot. Where a real OS 9 sample exists, diff against it.
