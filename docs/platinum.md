# Building exe's Platinum UI

exe's web UI is Mac OS 9's Platinum look, and so is every app that opens in
it. This is the shared knowledge: where the truth comes from, the numbers,
the colours, the blocks to copy, and the rules Livid has asked for by name.
The reference copies live in code — copy them verbatim rather than
restyling: `internal/server/ui/index.html` (the desktop),
`/www/exe-apps/Tides/index.html` and `Notes/index.html` (apps),
`internal/server/sysapps/bluepencil/index.html` (the pop-up menu button).

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
- Guest framebuffers are bitmap art: choose whole **device-pixel** scales,
  dividing by devicePixelRatio for their CSS size. A whole CSS scale becomes
  uneven pixels at Windows' 150% setting. Keep the viewer's input scale in
  sync, refit on density changes, and compare rendered pixels with the raw
  framebuffer, not just the window chrome. Shrink only if 1× cannot fit.
- Bitmap art in a bordered box (the Hub app's drawings, `docs/hub-drawing.md`):
  give the picture a whole number of device pixels a source pixel, its CSS
  size rounded **up** to the next 1/64px — layout keeps sixty-fourths and
  cuts the rest, and 256 pixels at 125 percent came out 255.996 wide. A
  panel centred on a veil is then moved onto the device grid (the canvas's
  rect times the density, rounded; the veil is fixed at the frame's corner
  and the browser sets a frame on a whole device pixel, so the frame's own
  place is left out). In a scrolling list the box can still stand between
  two device pixels: the picture's pixels are even blocks, its outermost
  rows and columns blend with the border. Neither of these is a way out:
  Chromium takes any border width under 1px as 1px, and an outline lies
  half over the picture. Measure on a screenshot of the whole viewport; one
  clipped to an element that stands between device pixels is resampled and
  shows blends the screen does not have.
- Bitmap art that moves (the Welcome window's header, a night sky over a
  row of Macs): draw it small on a canvas of its own and blit it, smoothing
  off, onto a canvas that holds one device pixel a cell (`width` and
  `height` the CSS box times the density, rounded; `image-rendering:
  pixelated`). An art pixel is 2 CSS pixels, so 2, 3 or 4 device pixels at
  100, 150 and 200 percent, and the nearest whole number at any other
  scale. Lay the scene out for the width it gets rather than scaling it,
  and stand it on the box's last row, so a row cut by an odd height is the
  sky's first. With the window half a device pixel off the grid the
  picture is still cell for cell; only its outermost column blends with
  the frame. The band's colour is the canvas's own background, so nothing
  flashes before the first frame. Ten frames a second on a fixed clock
  (`setInterval`, a step of 0.1s, so a frame is the same frame every
  time); stop on the bridge's `hide` and on a hidden tab; with
  `prefers-reduced-motion` draw one settled frame and start no timer.
  `~/tools/playwright/exe-welcome-test.js` reads the canvas back and
  compares it with the screen.
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
  secondary meta. Never 13px, never a system font stack. There is one line
  of display type: the Welcome window's heading, 18px bold on a 22px line,
  beside its 64px icon. Nothing else is set larger than 12px.
- On a Mac that stack lands on Geneva, which has one weight: a browser
  only thickens it a little, and a bold heading reads as the text under
  it. The public site (`site/site.css`) and the Platinum site template
  give Geneva a bold with two `@font-face` rules, `local("Geneva")` for
  normal and `local("Lucida Grande Bold")` for bold; where neither face
  is installed they come to nothing. The desktop does not have them yet.
  Linux and Windows fall through to a face with a bold of its own, so
  weight is judged on a real Mac, not in headless Chromium.
- Chrome is `user-select: none; cursor: default`. OS 9 had no pointing
  hand and no hover states: a control changes only while pressed (`:active`),
  menus alone highlight under the pointer (and, asked for by name, the
  Show All Windows tile under it).

## Controls, with the HIG's numbers

- Push button (`button.ghost`): 20px high, `#ddd` face, 1px `#262626`
  border, 3px radius, bold 12px, padding `2px 13px`. Bevel: `inset 1px 1px
  0 #ddd, inset -1px -1px 0 #777, inset 2px 2px 0 #fff, inset -2px -2px 0
  #aaa`. Pressed: `#666` face, white text, bevel `#444 / #888 / #555 /
  #777`. Disabled: `#777` text, `#888` border, no bevel. Notes adds `.danger`
  (`#7a1010` text) and `.armed` (red, the two-click delete); Todo, Weather
  and World Clock the `.sm` × of a list row; Tides adds `.on` (a depressed
  segment, `#888`).
- Default button: only the one Return triggers wears the ring, `outline: 2px
  solid #262626; outline-offset: 1px; border-radius: 4px` (the HIG's 2px
  black ring 3px out around a bevelled gap).
- Layout (dialog guidelines): buttons 20px high; OK and Cancel 58px wide, one
  width per set; 12px between buttons and 12px from any window edge; the set
  at the lower right, default rightmost, Cancel to its left; 4 to 6px between
  items in a group, 16px between groups; a checkbox is a 12px box with 5px to
  its label; pop-ups are 20px high with 6px between stacked ones.
- Static dialog text: the [HIG control layout guidelines](https://dev.os9.ca/techpubs/mac/HIGOS8Guide/thig-52.html)
  specify 16px fields for 12-point Chicago. Use a 16px line height for compact
  dialog copy; keep the standard button dimensions and margins.
- Text field: white, 1px `#262626`, no radius, 20px, padding `2px 5px`, the
  sunken frame as shadows `-1px -1px 0 #999` (top and left) and `1px 1px 0
  #fff` (bottom and right); focus is `outline: 2px solid #9999fe` at offset 0.
- Pop-up menu button: one shared block, `popup.css` in
  github.com/livid/exe-stats (from HIG fig. 2-7: 19px rounded black
  outline, white top and left, `#aaa` bottom, a 21px arrow well on the
  right with its own bevel and two 7px triangles, and a transparent native
  `<select>` inside). The figure is one row short: the HIG's layout
  chapter gives the pop-up the push button's 20 pixels, and a real Mac
  OS 9 draws 20 rows (the Monitors panel's Show pop-up, sampled from
  `~/.exe/mac-os9/modes-control-panel.png`, 2026-09-24: black, white,
  sixteen of face, `#aaa`, black, the triangles on rows 5–8 and 11–14).
  The hub sizes its menu to 20 (`.popup.lang`, triangles at `4px 3px`)
  beside its 20px buttons; the block itself is due the same move, for
  Weather and Blue Pencil. Apps and sysapps link it, `<link rel="stylesheet"
  href="/platinum/popup.css">` (Weather's units menu, Blue Pencil's
  options), and the hub's pages take it as `{{popup}}`, the way both take
  the chrome; the host gives the span its width. Never copy the block,
  never a bare select.
- A pop-up whose items carry icons (Analytics' host menu, each host
  beside its favicon): a native select cannot draw a picture in an
  option, so the shared block keeps its frame and well around a `button`
  in the select's place (the select's box said again for the button: the
  16px icon, then the name, ellipsised), and the list is a Platinum menu
  of the app's own, the contextual menu block with the desktop's
  `.dropdown.icons` columns: the 10px check mark 5px in beside the chosen
  item, the 16px icon at 17px, the text at 40px. It hangs 2px under the
  button at its left edge, at least as wide, clamped inside the window,
  scrolling past its foot. One row is inverted at a time, `.on`, moved by
  the pointer and the arrow keys alike; Return or a click picks, Escape,
  a press elsewhere or the window losing the keyboard puts it away. Every
  icon slot is 16px from the first paint, a stand-in glyph until the
  picture arrives. Pictures scale smoothly (a 180px touch icon at 16);
  glyphs of our own are pixels at whole scales and `geometricPrecision`
  between 1x and 2x.
- A field that grows with its text (the Hub composer, `fitField`): it
  opens at its shallow height and takes whole lines as the writing needs
  them, up to a limit cut from the window (half its height, a whole number
  of lines), where it scrolls as any field does; it gives the lines back
  the same way, and whatever empties it from script fits it again. Read
  the height from the field itself, laid out shallow with `overflow-y:
  hidden` for the moment of the reading — a scroll bar there narrows the
  lines and asks for one too many — and keep `hidden` while the text fits,
  so no bar flashes as a line is added. Fit on the window's `resize` (a
  narrower window rewraps; a phone's keyboard lowers the limit), never
  from a ResizeObserver on the field itself. The writer's own keystrokes
  moving the edge under the field is the thing asked for, not a layout
  jump; nothing else may move.
- Marks in a text field (the Hub composer's blue pencil): the textarea
  stays the field; a mirror div laid over it (`#marks`: the field's type,
  padding and `pre-wrap` wrapping, sized to the textarea's client box so a
  scrollbar rewraps both alike, scrolled with it, `pointer-events: none`,
  transparent words) shows only a 2px `--hl` `border-bottom` on an inline
  box round each marked stretch: a border, so it is crisp at 150 percent
  and sits in the line's leading without moving anything. A click is
  matched to a mark by the marks' client rects. What a mark offers floats:
  the app contextual menu (Blue Pencil's `.dropdown` block) hung 2px under
  the word, opening with a `.dd-head` line that never inverts, and never
  taking the focus from the field. A composer's "@" list (Hub's `#at`,
  `.at-item` rows: the 14px picture, the name, the id in 10px mono) is the
  same block on a layer of its own, hung 2px under the "@" as found in
  the field's mirror; its one inverted row is `.on`, moved by the arrows
  and the pointer alike, never `:hover`, so two rows never invert at once. A menu line that shows more (Show
  Rewritten Sentence) puts a second layer of the same kind at the same
  left edge but under the field's bottom edge, so the text it is compared
  with stays uncovered; its head is `.dd-head.wide` (360px at most, never
  wider than a phone), set 10px left of the rows so a sentence's choices
  hang under it; such a layer is pinned where it opens (`.dropdown.pin`)
  and scrolls inside itself past the window's foot. A count that opens a
  layer is a push button in the button row (the composer's `#sugg`: the
  pencil glyph and the figure, laid out as Find… is — a word there was
  the row's one long label and wrapped in a narrow window): it wears the pressed
  look (`button.ghost.open`) while its layer is up, as a pop-up's button
  does, toggles it, and never takes the caret. It comes and goes inside
  the row's flexible text slot, so no other button moves. The pass mark
  in that slot (`#c-status.ok`, "Proofread") is the check box's 10px
  check in `--green #1f9e33` on the text's baseline, the word in
  `#0f6b20`: the bright green is 3:1 on `#eee`, too faint for 11px type.
  A grey "nothing found" reads as a shrug; say a pass as a pass.
- Status bar: 15px total, a 1px black top border and a 14px `#ddd` face,
  11px `#333` text, `inset 1px 1px 0 rgba(255,255,255,.6), inset -1px -1px
  0 #aaa`, and `overflow: clip`. The clip is not decoration: on the body's
  1.45 line height the text's line box is 16px, taller than the face, and
  hangs a pixel past the bar and the window. In an app iframe that pixel
  is scrollable overflow of the page — a `scrollIntoView` or a focus then
  scrolls the page up a pixel while the fixed grow box stays, and the
  status line no longer meets the tile (a doubled black line over the Hub
  window's corner). Clipped, the overhang counts for nothing, and the text
  keeps its pixels: a `line-height` that fits the face moves the text down
  a pixel, out of line with the desktop's own status bars. Text only.
  Buttons never sit on a status line; they get a row of their own in the
  content area, laid out as above.
- Grow box: the 15px SVG sampled from OS 9, at the window's bottom right;
  its black top row lands on the status bar's line. An app streams
  `{exe:"grow", dx, dy}` through the bridge and the desktop resizes. The
  desktop tells the frame from `grow-start` and holds it until `grow-end`,
  never from each grow's `source`: Safari on iPadOS stamps messages posted
  from a pointer-captured pointermove handler with the desk's own window as
  their source, not the app frame (the trackpad grow moved nothing there
  until 2026-09-26, `exe-app-grow-source-test.js`). A fixed-size window needs no grow box: if the window cannot be resized
  (a dialog, an About box, a guest display shown at whole-number scales
  like the Mac OS 9 and Mac OS X apps) it has no tile, and an app says `"grow": false`
  in app.json so the desktop adds no edge grips either. Such a window has
  one size, the one its app.json names: the desktop applies no size the
  saved layout holds for it, and its zoom box, which switches a resizable
  window between two sizes, puts it back at that one (Welcome was once
  stretched to 1100px by its zoom box, saved so, and had no grow box to be
  dragged back by; `exe-zoom-onesize-test.js`). On a phone every
  app hides its grow box; the window fills the screen there.
- Tab control (`.tabbar` of `.tab`s over a `.tabpanel`, as Configuration,
  the VM window and the Log Viewer draw it): the active tab runs 3px down
  into the pane, over its border and bevel. A scroller inside the pane
  (the Log Viewer's printouts) is framed like `.listbox`: always a bar,
  the frame's right border dropped because the bar's black edge is that
  line, its last arrow the 14px borderless cell on the frame's bottom
  line. Tabs whose panes should keep their scroll place stack the panes
  in one box and hide the others with `visibility: hidden` — Chrome drops
  a `display: none` element's scroll offset. A printout that fills while
  it is read never moves the reader's line: an append goes below it; a
  trim of the oldest lines takes the height it removed off `scrollTop`
  (exact, since everything removed sat above the reader); a reconnect,
  which empties the printout and takes the backlog again, remembers the
  topmost line in view (with the two after it, as a log repeats itself)
  and how far down it sat, and finds it again as the backlog streams in.
  Only a wholesale change of content — a new filter — goes to the tail.
- A control panel with one thing to do (Apple → Software Update…,
  `#win-update`, after Mac OS 9's panel of that name, sampled from the
  QEMU guest on 2026-10-10): bold words on what it does at the top; one
  large bevel button centred under them, 52 by 50 — a black outline with
  square corners, inside it one row of `#ddd` then two of white at the top
  and left, one of `#777` then two of `#aaa` at the bottom and right, a
  32px icon on the `#ddd` face — with its name in bold 7px under it; a
  status of one or two 16px lines, centred; then an etched group
  (`.join-group`) around a sunken box, and a foot row with the "last
  checked" line at the left and a push button at the right. The large
  button is the panel's one action and its name says which (Update Now,
  Restart Now); pressed it takes the push button's dark face, and dead it
  loses its bevel and fades its icon. Every state draws in the same 400 by
  470: the status keeps its two lines and says only short things, and
  whatever is long — why an update stopped, what the installer said — goes
  in the box, which scrolls. Between the button's name and the status a
  12px slot holds OS 9's barber pole while exe is at work and how far
  along it is cannot be known: slanted bands of the scroll thumb's blues
  (`#ccccff`, `#6666cc`) in a sunken well, a 16px tile stepping its own
  width in eight steps a second, standing still under
  `prefers-reduced-motion`. It is the third motion on the desktop.
- A diamond says something wants attention, as it did in the Application
  menu: 7px, in a menu item's left gutter where the dot of a current
  choice goes (`.dd-mark.dia`), and right of the Apple in the menu bar,
  inside the title's own padding so the bar does not shift when it comes
  (`#apple-dia`). Stepped pixels at whole scales, geometry between them.
  Software Update wears both while a newer release is waiting.
- A manual of pages (Help → exe Documentation…, `#win-docs`): the paper
  shows one page at a time, the contents first — the manual's opening
  words, then its chapters as a numbered list of links, two columns where
  the window has the room (`columns: 2 200px`) and one on a phone — and
  after it a chapter each, under its name as the page's one `h1`. Under
  the paper a button row of its own (`#docs-nav`, the held row's
  construction: a 1px black top line as its seam with the paper, 12px all
  round, the status bar under it keeping its own line): Contents at the
  left, Previous and Next at the right, 72px each. All three are there
  from the first paint and go grey where they lead nowhere, and say where
  they lead in their tooltips, so nothing moves from page to page. Every
  page starts at its top. A chapter is a `## ` heading of `docs.md` and is
  named in an address as the website names it (`docSlug` is `mdSlug`, a
  Go test holds every heading to that), so one chapter refers to another
  by the website's address and the link turns the page in the window.
  `openDocsWin(name)` opens a chapter by its heading. The paper's default
  height is the contents page's own, so every chapter's name shows and the
  scroll bar has nothing to do. It is measured (`docsFit`, into
  `--docs-h`), not a figure in the CSS: no font ships with the desk, so
  the page is as long as its words run in the reader's own face at the
  window's own width. The window is open at once and drawn when that page
  is on it (`.unread`, a second at most), so it never changes height in
  view. A height it was dragged to is the reader's and is over the
  measured one; the snapshot keeps it as `dh`, and only then — a window
  whose height is its content's saves none, as Virtual Machines does.
  Over the paper a search bar (`#docs-find`): the Search window's query
  bar, one rule for both — 8px round a field as wide as the frame, a 1px
  black bottom line as its one seam with the paper — there from the first
  paint, its field dead until the manual has come. Words in the field
  narrow the contents to the chapters that hold every one of them, in any
  case and inside longer words: the page's `h1` says in how many, and the
  chapters keep the contents' own numbers in one column
  (`ol.docs-found`), each with its name, the count of its found words in
  11px `#777` beside it, and up to three of its paragraphs as a line each
  — some 180 letters round the first found word, cut at spaces, a
  heading's line in bold, a line a link in the text's own colour. Every
  found word wears the Hub's yellow (`mark`, `#ffff66`, the text's
  colour), in the list and on any chapter turned to while the field holds
  words; a mark changes no measure of its line. A name opens its chapter
  at the top; a line opens it with that word a quarter of the way down the
  paper, a found heading at the top. While the field holds words, Contents
  and Return lead back to the found chapters; emptied — Escape does it, as
  in the Log Viewer's filter — the field gives back the page the search
  was begun from, where it was being read, or leaves a found chapter on
  the paper without its marks. While the field holds anything a clear
  box stands at its right end (`#docs-x`): a 13px disc of `--g700` with a
  white cross of seven pixels a stroke, 5px in from the field's edge in a
  box 23px wide and the field's height, and the field keeps that room
  clear of its text whether the disc is there or not, so no letter moves
  when it comes. Pressed it goes to `--black`; a press empties the field
  as Escape does and leaves the keyboard where it was, in the field or
  out of it (no keyboard rises on a phone, where a finger has 8px more
  round the box). It is off the Tab round: Escape is its key. Stepped
  pixels at whole scales, a copy in geometry between 1x and 2x, where a
  one-pixel diagonal smears. The window never changes height for any of
  it, and the default height's limit gives up the bar's own, so a short
  screen's window ends where it did.
  A chapter's page wears, left of its name, the icon of what it is about
  (`docsIcon`, `DOCS_ICONS` by the chapter's name in an address): the
  desktop's own art at 16px as the Windows lists show it (`sizeSvg`, the
  `.dd-ico` rule's declarations) — the VM's Mac for Virtual machines, the
  folder for the Workspace, a system app's own icon for its chapter. It
  stands in the name's left margin, 22px that are there before the art
  is, 2px down so that it sits in the middle of the name's first line; a
  name that wraps runs on beside it. The art is read when it is drawn, so
  a repaint in the Icon Editor shows on the open page, and a page drawn
  before the app list came gets its app's art when it does. Five chapters
  had nothing on the desktop to wear and got icons of their own, drawn on
  the 32 grid and registered like any other: a pop-up menu with a line
  chosen (`deskmenu`), a brass key (`sshkey`, the palette's key-brass),
  Software Update's Mac with its arrow turned upward (`publish`), two
  Macs on one cable (`join`) and a control panel with two sliders
  (`config`). A chapter the list does not name wears the document, and
  `TestManualChaptersHaveIcons` holds the list and the manual's chapters
  to each other. The contents and the found chapters wear none. A manual's
  heading still reads as the chapter's name alone: the art is hidden from
  assistive readers and carries no text.
- A pad of pages (Notes, `/www/exe-apps/Notes`): Mac OS's Note Pad,
  sampled from the guest's Apple Extras. A white sheet flush with the
  frame, its bar running down to the grow box; under the sheet's bottom
  line two page edges, `#eee` then `#444` twice, then `#eee`; the page
  number centred on the window's width with its baseline on the grow box's
  top line; at the bottom left a 23px dog-ear under a black top line and a
  black right line that stops one row short, the flap's real pixels above
  the fold and the next page's `#eee` below it, a vector copy at
  fractional scales. The flap turns forward, below the fold turns back.
  Its notes are never a list: the toolbar's Corkboard button (pressed
  while up) shows every note as a sheet pinned to cork, a 64px tile in the
  system palette's cube colours, pixels whole (OS 9 shipped no cork
  pattern). A sheet is white with a 1px black edge and a hard 2px shadow
  at 35% black, nudged whole pixels off the grid and never rotated, its
  13px pixel pushpin (vector between 1x and 2x) and nudge picked by a hash
  of its id; no sheet is marked as the open one. A note may take a
  Stickies colour (sampled from Mac OS 9's Stickies: its pastels, the
  loud yellow and blue a step lighter, all in the 256-colour cube): the
  pad's sheet, its board card and the dog-ear's flap (the sampled greys
  multiplied by the tint) take it, and the next page's tint shows under
  the fold. It is chosen from a row of small squares at the right of the
  sheet's foot, 5px short of the scroll bar, never in the toolbar: each
  square its tint inside Stickies' edge colour, the chosen one ringed 2px
  in `--hl`. The note's writing has the toolbar's 10px of room at the top
  and sides; the foot carries, left to right, the dog-ear, the note's date
  in 11px `#777` (the longest form that keeps 16px from the page number,
  else none), the page number and the squares. With a note open the
  toolbar carries − + for the text's size (never 13px) and a Sans Serif |
  Serif segment (joined push buttons, the chosen one pressed; the serif
  Charter, then Georgia), and gives way a step at a time where the words
  will not fit: "Aa" samples for the faces, Corkboard as its pushpin, then
  tighter sides and gaps.
- Scrollbars: the pixel-sampled 15px block (track `#777 #888 #aaa #bbb
  #ccc`, thumb `#ccccff #9999ff #6666cc` with the ridged grip, 16px buttons
  with 8x4 arrows, only the trailing pair, the `scrolled-y` and `at-y-end`
  end merges). Copy it whole. Against a frame the bar has no trailing
  border; the frame's line is the bar's edge. The block's last lines give
  each merge class a custom property of its own (`--sb-at-y-end: 1` and
  so on), and they are not dead weight: Safari redraws a custom scroll bar
  only when its element's own style changes, and a class that touches only
  `::-webkit-scrollbar` pseudo-elements changes none of it, so the merges
  stayed stale there and the thumb at the end doubled the line against the
  arrows (Livid's catch, 2026-10-09). Chrome restyles either way. Borders
  on `::-webkit-scrollbar-track-piece` would make the merges static, but
  Chrome never paints them on an active bar. Playwright's WebKit
  (`webkit.launch()`) shows the same as Safari; check a scroll bar there.

## Chrome the desktop draws

- Window: `#ccc` body, 1px `#262626` border, hard shadow `2px 2px 0`, inset
  white and `#999` bevel. Title bar: 13px boxes (close, zoom, shade) with a
  2px inner black ring, stripes of 2px rows `#fff / #777` on `#ddd`, a bold
  12px title on a `#ccc` field. An alert has a red-striped bar (`#fcc`
  field, `#fff / #f66` rows, `#f99` bevels), no title and no close box, and
  holds only an icon, a bold label beside it, a plain narrative and buttons.
  `ui/offline.html`, the alert the service worker shows when the daemon does
  not answer, carries its own copy of the window, alert-bar and button
  blocks (it must render with nothing else reachable). It names the apps
  that keep a copy on the device and gives each a button, Open and the
  app's name, left of Try Again, which keeps the ring; it is shown only
  once it knows them, so it comes up at the size it stays. The Hub app
  (`sysapps/hub`) carries the window and alert-bar blocks for the alert it
  raises over itself when the hub refuses a file or a post: change them in
  all three. An app's alert sits on its dialog veil, 380px at most and
  16px from a phone's edges, and holds the whole message, selectable.
- Stickies (a note stuck to the desk, `.sticky-window`), as Mac OS 9's
  Stickies draws a note — sampled in the QEMU guest on 2026-10-14, every
  colour through its Color menu. The window is the sheet in the note's
  tint with a 1px frame in the tint's edge, no bevel and no shadow. The
  title bar is 9px of a 1px checkerboard dither, edge and tint, the edge
  at the top-left; in it a 7px close box 4px in from the left and a 7px
  collapse box 4px in from the right, each a well of the tint with its
  glyph in the colour's ink (close `M6 0h1v6H6zM1 1h4v1H1zM1 2h1v3H1zM0
  6h7v1H0z`, collapse `M6 0h1v6H6zM4 1h1v1H4zM3 2h1v1H3zM2 3h1v1H2zM1
  4h1v1H1zM0 6h7v1H0z`). The text follows with no line between, 4px in
  from the frame and right under the bar, and the note has no scroll
  bar. An 8px size box sits on the bottom-right corner over the frame
  (`M4 0h4v1H4zM4 1h1v3H4zM7 1h1v6H7zM0 4h5v1H0zM0 5h1v2H0zM0 7h8v1H0z`).
  Inactive, a note is the sheet alone: no dither, no boxes, no size box,
  and only the right and bottom lines of its frame. Tint / edge / ink:
  yellow `#ffff99 / #ffcc00 / #ff9900` (the desk's Notes keeps its
  lighter `#ffffcc`), blue `#99ffff / #00ccff / #0099ff` (`#ccffff`
  here), green `#ccffcc / #66ff66 / #00ee00`, pink `#ffcccc / #ff9999 /
  #ff66ff`, purple `#ccccff / #9999ff / #9999ff`, gray `#eeeeee /
  #bbbbbb / #777777`; white is Note Pad's, not Stickies', and takes
  `#999999 / #777777`.
- Every title bar has a menu, dialogs' too: a right-click, or a press held
  550ms on a touch screen (`winMenu`). Its one item, **Save a Screenshot**
  (`shotWindow`), draws the window a second time from a copy of its markup
  and the document's style sheets inside an SVG `foreignObject`, at a whole
  number of pixels to the point, and writes that density into the PNG as
  Apple does (`pHYs`, 72 to the inch a pixel: `shotDensity`), which is what
  makes Preview show a 2x picture at the window's size. So a window comes
  out right when what it
  shows is said in markup and style sheets. Carried over by the code:
  typed text, ticks, scroll positions, the focus ring, a canvas, a movie's
  frame, a same-origin frame. Not carried: `:hover`, a selection, a
  sandboxed frame, a WebGL canvas that does not draw every frame, and a
  control left to the browser's own theme (`appearance: auto`), which a
  picture draws its own way. Safari puts every `box-shadow` in the wrong
  place when such a picture goes straight onto a canvas (the bevels vanish
  at 2x), so there it goes through an `ImageBitmap` first
  (`shotShadowsBent` asks once which browser this is). A new window kind is
  checked against the browser's screenshot with
  `~/tools/playwright/exe-screenshot-compare.js`.
- A modal dialog that asks for something (the desktop's API Token, raised
  by itself when the API answers 401): a title, no close box, 380px at
  most. A bold line says why it is there, the guide follows in 16px
  lines, a command to run stands apart in the mono face and can be
  selected. Under its 20px field one status line is always present: what
  the field is for, then what became of the answer (`#7a1010` when it was
  refused), each short enough for one line at 320px in a phone's wide
  face, so the dialog never changes height. Cancel and OK are 58px, 12px
  apart, at the lower right; OK asks the daemon first and the dialog
  stays up on a no.
- Menu bar: 20px `#ddd`, inset white top and left, `#999` bottom and right,
  1px black bottom line; bold titles with 10px side padding; an open title
  inverts to `#333399`. Menus: `#eee`, 1px black, hard shadow `2px 2px 0
  rgba(38,38,38,.85)`, items `3px 22px 3px 18px`, hover inverted, disabled
  `#888`, separators a 1px `#999` line over a 1px white one. Contextual menus
  are `#ddd`. A line that is there to be read rather than picked (the price
  module's Recent Moves) is `dis` plus `info`: inert, never highlighted, but
  in the normal text colour; heads, footnotes and empty states stay `#888`.
- Desktop icons: 32x32 pixel art at exactly 32 CSS px, 11px white labels
  with a 1px black text shadow; selected is a `#333399` label and a darkened
  icon. Window lists show the 32px art scaled to 16.
- Control Strip (`#cstrip`, bottom-left, 24px, rebuilt pixel for pixel from
  OS 9): black outline, 13px sunk scroll wells, module tiles behind 1px
  separators (face `#c0c0c0`, white top/left L, `#a0a0a0` inner and
  `#808080` outer bottom/right L, a 16px icon at x2 y3, the solid 4x8 menu
  triangle 3px from the right edge), the 18px tab. A standard tile is 30px;
  a wide module (`.cs-mod.wide`, the Cloudflare heartbeat with its bytes
  a second, the price ticker, the agent usage meter and the activity
  module, as OS 9's battery gauge was wider) keeps the frame and bevel
  around a longer face and shows its 11px figure right-aligned in a slot
  of fixed width. Size that slot to the widest figure it can show and no
  wider — a gap ahead of the figure reads as a tile that is too wide. The
  font stack ends in whatever the system has, so measure the widest
  string in the running font before the first paint (`auFit`: string +
  2px, plus 34px of frame, icon and triangle) rather than fixing a pixel
  width tuned on one machine. A module that charts (the activity
  module) draws in a white well behind a sunken frame's two Ls — `#808080`
  top and left, white bottom and right, each a border — on a `<canvas>`
  sized in device pixels, one sample a CSS-pixel column with its edges
  rounded to whole device pixels, so the columns stay crisp at 150%. A
  module's menu is a
  contextual menu whose left edge sits on the tile's separator and whose
  bottom line runs two rows into the strip; its current choice wears OS
  9's dot (`mark` on the item), not a check mark: the real module menu
  (9pt Geneva, text 17px in) puts a 5px bullet 3px in from the border and
  10px short of the text, two rows below the cap top and one above the
  baseline; scaled to the 12px menu that is a 6px dot with its top 9px
  down the 24px row, centred sideways in its column (7px to the border
  and 7px to the text's ink; 6px each side of the coin in a panel with
  icons) rather than hugging the border as OS 9's does — Livid read the
  OS 9 spacing as unbalanced. A crispEdges sprite at integer scales, drawn
  `geometricPrecision` between 1x and 2x so 150% shows a round dot and
  not 7-then-8 device rows with a fringe.
  A module whose icon shows state (the Cloudflare lamp, the Tailscale
  panel's lit lamps) keeps reserved colours in its art that the code
  repaints per state — the icon stays one editable drawing. A pair of
  marked lines (Tailscale Active / Inactive, as OS 9's AppleTalk Switch
  module had) is how a module offers an on/off switch, not one line that
  changes its words. A module tied to something the machine may lack
  (Tailscale) hides its tile when the daemon says so and remembers that
  per browser, so the strip has its final width from the first paint.
- Show Desktop (the menubar button left of Show All Windows, inverted the
  same way while it is up): every open window slides off the desk to its
  nearest edge, 8px of it left in view, by the same `transform` and 200ms
  flight as Show All Windows below; the desk, its icons and the Control
  Strip stay in use. A parked window is one thing to the pointer
  (`pointer-events: none` on its children), and a press on its sliver
  brings them all back with that one in front, as do the button, Escape,
  and anything that opens, closes or re-lays a window.
- Show All Windows (the menubar button left of the magnifier, inverted
  like an open menu title while it is up): every open window flies into a
  grid on a field inset 24px from the desktop, 24px between tiles, 40px
  kept clear above the Control Strip. The column count is the one whose
  worst-fitted window comes out largest (a window never grows past scale
  1; ties go to the larger mean); tiles keep their spatial order, top row
  first, and a short last row sits centred. Windows move by `transform`
  alone, origin at their corner, so the saved geometry is untouched; the
  desktop icons step aside (`#rail` hidden) and a shield over the desktop
  takes the pointer. The tile under the pointer wears the wash
  (`.sa-hover`): `--hl` at 30 percent with a 1px `--hl` border and the
  window's name centred in bold 12px white with the icon labels' 1px
  black shadow; its colours fade in and out over 150ms (the name with
  them), off under `prefers-reduced-motion`. A pick, the desk, the
  button or Escape puts everything back.

## Seams, stability, phones

- Every seam where two pieces of chrome meet shows exactly one 1px dark
  line. When both sides bring a border, drop one. The stack is black line,
  then white highlight, then face.
- Nothing loaded later may move what is already on screen: paint the final
  structure as placeholders and fill it in place.
- A phone runs one fullscreen window under the 20px bar, keeps the safe
  areas and the home indicator clear, and shrinks a button row to glyphs
  when words will not fit (the Hub's Find, Refresh, Profile). A tapped
  desktop icon launches its window the way a home screen does: the tile
  scales to 1.35 and fades to nothing over 120ms ease-in, then the window
  covers it and the tile resets unseen (`launchIcon`). That, and the 200ms
  flights of Show Desktop and Show All Windows, is all the motion on the
  desktop but for Software Update's barber pole (above, Controls); all of
  them honour `prefers-reduced-motion`. In an app the one motion so far is Notes'
  zoom between its corkboard and a note (240ms ease-in-out: the sheet grows
  from its card's rectangle and fades in, and shrinks back into it), which
  honours it too and leaves nothing transformed. Welcome has two: its
  pages slide (a feature's card comes in from the right as the first page
  leaves by the left, 220ms `cubic-bezier(.3, .7, .2, 1)`, and back the
  other way; Web Animations with no fill, so nothing stays transformed,
  and with less motion the other page is simply there), and its header
  moves on its own (above, Pixels at every scale). The header stays where
  it is while the pages slide: a page is the whole window with its first
  120px clear, and its 64px icon rides across the header's bottom line
  with it.
- A window that greets (Welcome, after the Planet app's welcome screen): a
  dark picture across the top, the icon at twice its size across the
  picture's bottom line, one heading and a line under it, the features two
  to a row — a 32px icon, a bold name, a blurb in 11px `#444` — and a
  button row under a separator: a button at the left, a check box in the
  middle (Show this window when the desktop opens), OK at the right,
  wearing the ring. A card's Back wears a chevron, the Hub's
  glyph-and-word layout (the glyph 9px in, 5px to the word): 5 by 7 with
  2px strokes, the weight of the bold word, on the caps' middle, in
  `currentColor`, stepped at whole scales with a smooth copy between 1x
  and 2x. A feature is pressed the way a desktop icon is picked
  (`--hl`, white words, the art darkened) and has no other state. What a
  card says of this machine keeps two lines from the first paint; the
  green check beside a ready feature's name keeps its 10px.
- A wizard (the Hub app's Join a Hub, `#join`): a task of several steps
  that a new user is walked through takes the app's whole face over its
  status bar, never a panel on the veil. A head: the app's 32px icon, the
  task's name in bold, one line under it in `#555`. Down the left, 132px
  behind a separator, the steps by name on 20px rows, read and never
  pressed: a done one wears the pass mark's green check, the one in hand
  OS 9's 6px dot and bold type (stepped pixels at whole scales, a circle
  between 1x and 2x), the ones to come are `#777`. Beside the list the
  step's page: a bold line that says what the step asks or shows, 16px
  lines of copy, the app's own rows and fields, 420px wide at most. Under
  a separator the button row, 12px all round: Cancel at the left, Go Back
  and Continue at the right, as the Installer had them, Continue wearing
  the ring and named for what it does on the last step (Join). All three
  are there from the first paint: Go Back is grey on the first step,
  both grey while the wizard waits for an answer. Return presses the
  ringed button, Escape is Cancel. A page asks or shows one thing, and
  nothing is kept before the last step's button. A line under a page's
  fields (`.j-note`) keeps two lines and says what the wizard is doing
  and what came of it, `#7a1010` when that was a refusal; a button of the
  page's own (Check Again) has a row with what it did in words beside it,
  two lines kept; a box whose words change when the button is pressed
  keeps the height it had, so the button stays under the pointer. Under
  460px the list gives its room to the page and the head's line counts
  instead (Step 2 of 4: The Hub). A phone's keyboard is not raised by a
  page's first field. What is behind the wizard keeps its place, hidden
  (`visibility`, since a `display: none` scroller loses its place), for
  whoever cancels; with nothing behind it, Cancel puts the window away.
  `~/tools/playwright/exe-hub-join-test.js` walks it against a mocked hub.
- A wizard in a window of its own (Extras, `sysapps/extras`, from
  Welcome's Apps card): the same head, list, page and button row with no
  status bar under them, in a fixed-size window with no grow box, sized
  so that its tallest page fits without a scroll bar (580 by 500). The
  tallest is the page of a thing that is not on the machine: three lines
  of text, two before the agent's line, three of it, and under the field
  two for what Find says, which are room the window leaves and not
  height the page asks for. Count the lines in Safari on a Mac, where
  Geneva and SF Mono run wider than a Linux test host's faces: a line
  more of each is a scroll bar there that no Linux picture shows. Its
  steps are a page for each of four things that can be on or off, and a
  last one, Review. A page says what the thing is, then under a
  separator how it stands on this machine — a bold line and two lines
  kept for the answer, under the green check when it is on and nothing
  is against it, `#7a1010` when something is — and has one check box,
  which is the choice. A line under the box, two lines kept, says what
  the last step will do while the box differs from the machine. A thing
  that cannot be chosen has its box dead and its label `#777`, and in
  the line's place how to get it: the words for a coding agent in a
  sunken white box (mono, selectable), Copy Prompt in a row with what it
  did beside it, and a 20px field with Find on one row for the folder of
  a copy exe did not find. Review lists each thing on a line, its name
  bold in an 84px column and what becomes of it beside it, `#777` where
  nothing changes. Its button is Apply while there is a change to make
  and Done when there is none; after Apply each line says what came of
  it, under the green check or in `#7a1010` with the daemon's own words,
  and the button says Apply again while something is left. Put away by
  its close box, the wizard starts over the next time. Its icon is a
  jigsaw piece, as Mac OS drew its extensions.
  `~/tools/playwright/exe-extras-test.js` walks it.
- A wizard with nothing to apply (Agent Setup, `sysapps/agents`, opened by
  **Set Up…** on Welcome's Claude Code and Codex card, named short so the
  card's five buttons fit one row): the same window (580 by 500, no grow
  box), a page for each of the two CLIs and Review, whose button is Done.
  A page says what the CLI is, then under the separator how it
  stands here — Installed with its version and path under the green
  check, in `#7a1010` when it is there but would not say its version —
  and then one of two things. For one that is installed, a bold line
  and four bullets on what exe adds, and its Open button in a row with
  a line of words beside it. For one that is not, the way onto this kind
  of machine: a line of lead, the command in the sunken mono box, the
  other way in 11px `#555` under it, Copy Command and Install in
  Terminal… in a row with what they did beside them, a line on the
  sign-in after, and Check Again on a row of its own with its answer on
  the line under (one line; two are counted as room, as Extras counts
  Find's). Review is a row for each CLI and one for Sessions (tmux),
  under the check where it is so and `#777` where not. The buttons that
  open things go through the desk bridge, which lets a system app open
  a terminal on a command (`terminal`) and have the desk look at the
  machine again (`refresh`), so a CLI installed while the desktop is up
  gets its icon. Its icon is the agents' compact Mac, its screen showing
  an arrow coming down onto a line.
  `~/tools/playwright/exe-agents-test.js` walks it, the desk half with the
  desk's actions stubbed, since a real Open would start the CLI and a
  real Install would run an installer on the host.
- A stack of screens on a phone (Projects, `/www/exe-projects`): an app
  of columns shows them one at a time, each filling the window over the
  status bar, which stays. The screens lie over one another in one box
  (`overflow: clip`, so no `scrollIntoView` can shift it) and the ones
  that do not show are `visibility: hidden`, so each keeps its scroll and
  its selection: coming back, a screen is where it was left. Each screen
  has a bar of its own at the top, so a screen is whole wherever it
  stands while two of them move. The bar is the held row turned over:
  44px of `#ddd` over a 1px black line, 12px all round a 20px row. At
  the left Back, under Welcome's chevron (the glyph 9px in, 5px to the
  word), named for the screen it leads to and never wider than a third
  of the bar (an ellipsis); in the middle the screen's name in bold,
  centred on the window while the sides leave it the room (a grid of
  `minmax(max-content, 1fr) minmax(0, auto) minmax(max-content, 1fr)`,
  8px between) and cut with an ellipsis before any button gives way; at
  the right the screen's one main action, a push button in words. A
  button that makes something wears an 8px plus alone (2px strokes, the
  chevron's weight, in a 30px button) under 360px. A row's own action
  (Projects' Open in Claude Code) is a 30px glyph button at the row's
  right, 12px in, centred on the row: the glyph in the button's ink with
  the ground showing through (the Hub's `.k` and `.w`, which goes dark
  with a press), dimmed as the row's menu item is, and only in the desk,
  where there is a window to ask. What else a screen
  can do has a row at its foot, where a thumb is: the held row itself (a
  1px black top line, 12px all round), a pop-up at the left at the
  button's 20px, buttons at the right, the status bar under it keeping
  its own line. A field that is written in from that screen (a comment)
  is such a row too: one line deep until it is written in, growing as
  the Hub's composer does.
- It moves like this. Going deeper pushes: the new screen comes in from
  the right as the old one leaves by the left, and Back pops the other
  way; 250ms, Welcome's `cubic-bezier(.3, .7, .2, 1)`, two Web Animations
  of `transform` with no fill, so nothing stays transformed, and a newer
  move ends a running one where it was going. The seam between two
  moving screens is one black line: a `box-shadow: -1px 0 0` outside
  each screen's left edge, off the window at rest, the deeper screen
  lying over the other. A tapped row is picked at once and the screen is
  pushed when what it will show has been read, so the screen that comes
  in is whole and nothing on it jumps. A change of screen under a sheet
  is not animated. A drag from the window's first 24px pops: told from a
  scroll after 8px (more sideways than up or down, and rightward; the
  scrollers carry `touch-action: pan-y pinch-zoom`), the screen is then
  under the finger with the one before it beside it. What it does when
  the finger lifts goes by how the finger was moving at that moment, over
  its last 100ms: going right faster than 0.3px a millisecond it pops (a
  flick, however long the finger rested first), going left it stays (a
  drag taken back, however far it went), and a finger that was still or
  slow leaves it to where the screen stands, popping past half the
  width, over what is left of the 250ms. When it stays, or when the
  browser takes the touch away or a second finger lands, it goes back
  over 160ms. With
  `prefers-reduced-motion` the other screen is simply there; the drag
  still follows the finger and lands at once. The window opens again on
  the screen it was left on (per browser), with no slide. This is the
  fourth motion in an app, after Notes' zoom and Welcome's two.
- A finger on such a stack: a button stays 20px and answers over more,
  an undrawn `::after` pad out to the bar's edges in a button row and
  8px elsewhere, never past half the 12px between two buttons; a row
  with a 32px icon is 44px, a menu's line 32px. No scroller draws a bar.
  What a right-click gives on the desk, a press held 550ms within 8px
  gives: the same `contextmenu` event is dispatched at the finger (to
  whatever is under it by then, since a list may be drawn again under a
  resting finger), so the menu is the desk's own; a browser's own event
  makes ours stand down, and the click that follows the lifted finger is
  swallowed wherever it lands, since the menu came up under it. Where a
  hold must give a menu, the text is not selectable (the menu copies it).
- A sheet on a phone fills the window and comes up from its foot (the
  same 250ms; back down the same way; neither with less motion): its
  name centred in a bar of its own, the form scrolling between two lines
  with its names over its fields, and at the foot the button row with
  Cancel and the default alone, 12px in and 12px apart. What else the
  row held on the desk (Delete…, Archive, Discard Draft) is the form's
  last group, under a separator. No field takes the keyboard by itself.
  The keyboard lifts the row with the window: as a window of the desk
  the frame is already the visual viewport's height; as the page itself
  the body takes `visualViewport.height` (`--vvh`) and the pan is
  undone; either way the field being written in is kept in view. Type
  stays 12px: an iPhone is kept from zooming by the desk's
  `maximum-scale=1`, said again by an app opened bare. Safe areas are
  the desk's; only a bare page pads itself (Dict's rule). A line that
  says how an upload goes puts the figure first ("40% of name"), and one
  that says what went wrong takes a line of its own over the buttons
  while it is there. `tests/phone-test.js` in exe-projects walks all of
  it in Chromium and WebKit. None of it has been held in a hand yet: the
  drag's feel, iOS's own long press and the real keyboard are still to
  be tried on an iPhone.
- A question asked in place (Projects' drafts): when the daemon refuses
  a save because the same draft was changed on another desk, the sheet
  keeps its words in its fields, saves nothing, and asks which to keep in
  the line over its buttons, in the text's colour and not the error's,
  with the two answers (Use the Other, Keep Mine) standing where the
  button they displace stood (Discard Draft in the sheet, Attach… in the
  comment row). Nothing is a modal alert, so no Return or Escape pressed
  out of habit can throw words away. On a row too narrow for the words
  the question takes a line of its own over the buttons.
- A picture window on a phone is put away by swiping it down: the window
  follows the finger from 6px of vertical travel (a sideways drag and a
  second finger stay the browser's), springs back over 160ms under 90px,
  and closes past it or on a flick of 40px inside 300ms. A drag the
  browser cancels springs back however far it went — a cancelled touch is
  not a dismissal — and the gesture stands down entirely while
  `visualViewport.scale` is above 1, since a pinch-zoomed page pans with
  one finger and the desktop allows the pinch. What the window covers
  shows through the gap while the finger is down, and since a phone
  window borrows the menu bar's bottom border for its own top line, a
  window off the bar wears that line itself — drawn as a shadow above the
  frame, so nothing inside shifts by the pixel a border would take.
- A tmux-backed terminal (the agent windows, and a Terminal on a host
  with tmux) has no scrollback of its own, so a finger drag there turns
  into synthetic wheel notches, one per
  cell travelled, that take the wheel's own road (tmux copy mode, or the
  CLI's mouse reports); a flick glides on with a per-ms decay of 0.995.
  The box carries `touch-action: none` so the page never pans instead.
- A list whose rows drag to reorder (Todo, Weather) is the opposite case:
  a finger must still scroll it, so the rows keep the default
  `touch-action`. A mouse picks a row up after 4px; a touch only after
  resting 500ms within 8px (the phone's long press), and from then a
  non-passive `touchmove` on the list calls `preventDefault` so the
  browser does not take the pan. Moving sooner is a scroll (the browser
  sends `pointercancel`), a quick lift a tap.
- A drop target's ring is a 2px `--hl` inset shadow: on the Finder's icon
  area, the desktop, and over a terminal's screen as an `::after` overlay
  (an inset shadow on the box itself hides under xterm's canvases). A
  terminal window's held row (`.held`, the paths of a drop that landed
  while the link was down) is a row of its own under the terminal, 12px
  padding, its text ellipsised, three 68px buttons 12px apart at the
  right, Insert dead until the socket is back; its top line is its seam
  with the terminal and the status bar keeps its own.
- A list beside a terminal (the agent windows' session column): a 160px
  sunken white list of 18px rows, the selected row `--hl` with white
  text, its one seam with the terminal the 1px line on its right — a 6px
  grip astride it drags the list's width, saved in the layout — and its
  buttons in a row of their own under the list: New, and in the Claude
  Code window Search beside it, 58px each and 12px apart, centred. A
  column under 153px cannot hold both words, so Search is the Hub's 13px
  magnifier alone in 31px (`.sess-col.tight`), and under 122px New gives
  up width too; a phone's strip always wears the glyph. The column keeps the
  terminal's height (`contain: size`) and the list scrolls inside it; a
  long list never grows the window. When the current session changes the
  list scrolls its row into view, the least distance that does it, and
  otherwise stays where it was scrolled. On a phone the column turns
  into a strip of tabs above the terminal, sliding sideways to the
  current one the same way. A row's mark sits left of its
  title: the 7px black dot when the session wants someone (a hook's word,
  or the bell), or the Chat list's 6px pulsing green dot while it works
  unwatched — green means "live" everywhere.

- A terminal window's key row on a phone (`.keyrow`, `keyRowButtons`):
  the keys a phone's keyboard has not, in a row of their own under the
  terminal and over the status bar — Esc, Tab, Shift-Tab and the four
  arrows, seven ghost buttons sharing the width, 8px apart and 12px from
  the edges, 20px tall in a 32px row whose top line is its one seam with
  the terminal. Shift is the Mac's hollow arrow, 7 by 8, before its
  word; the arrows are the scroll bars' 7px triangle on a stem, all in
  `currentColor`. A finger gets the row's whole height and half of each
  gap through a `::before`; the button keeps its 20px look. A press
  takes no focus (`pointerdown` cancelled), so the keyboard stays up,
  and raises none when it was down. Off a phone the row is `display:
  none`. `~/tools/playwright/exe-term-keyrow-test.js` checks it.

- A modal dialog of a window's own (the Claude Code window's Search,
  `.sess-veil`): a veil of the overlay's 15% black over the window's
  frame, so its title bar stays free, the desk's other windows go on
  working and the dialog travels with its window. On the veil, centred,
  the modal dialog's box: a title, no close box, 440px wide and never
  nearer than 16px to a phone's edges. Inside, the Search window's query
  bar with the 20px field; a white list of the column's 18px rows,
  fourteen of them from the first paint so the box keeps its height
  whatever is found, a short window taking rows off and never the
  buttons; and a button row with a count in 11px at its left and Cancel
  and Open at its right, 58px each, Open in the ring. The list is
  `.vflush`: the frame's border is its bar's trailing line and the button
  row's top line is its last arrow's. A row is the column's: the mark in
  a 7px slot, the title, and at the right in 10px `#777` the folder and
  the day; a rule parts the sessions here from the conversations that
  are not. A press outside the field takes no focus, so typing always
  narrows the list; a click selects, a double click or Return opens,
  Escape is Cancel, and Tab goes round the dialog only. The box is not a
  `.window`, so the window lists, Show All Windows and the saved layout
  never see it. `~/tools/playwright/exe-claude-search-test.js` walks it.

- The Claude Code window's prompt line (`.sess-last`): a 20px band in
  the column's grey over the terminal, holding the first line of the
  session's latest prompt and, at its right in the Search rows' 10px
  `#777`, the count; `#777` words while there is none. Its 1px bottom
  line is its one seam with the terminal, the column's right line its
  one seam with the column. A click opens Prompts, the Search box again
  but with a close box at its title bar's left (Livid asked for the tile,
  2026-10-14; the stripe on its right runs to the edge, as on a window
  with no zoom or shade box): a white list of 18px rows — the number in a slot as wide as the count,
  the first line, the time at the right — fourteen from the first paint,
  a 1px line that is the list's last arrow's, then the selected prompt
  whole in a sunken white box six lines tall (98px, selectable,
  scrolling), and the button row with the count, Copy and OK in the
  ring. `~/tools/playwright/exe-claude-prompts-test.js` walks it, the
  phone's layout too.

## The Workspace as a list

- A Workspace window is icons or a list (`renderFinder`): **View** in the
  white-area menu, the check mark on the view in force, as the Finder's
  own contextual menu has it. The list was sampled from the Finder of the
  QEMU Mac OS 9 and agrees with the HIG's fig. 2-43.
- The header is a row of cells 19px high, between the status line's rule
  and a rule of its own. A cell is outlined left and right, so there are
  two black pixels between two cells and beside the frame. This is the
  one exception to "one seam, one line", and it is the Finder's. A cell's
  face is `#ddd` under a white top and left and an `#aaa` right and
  bottom. The column the list is sorted by wears `#666` under `#555` and
  `#777`, its label still black, and is `#ddd` down the list where the
  others are `#eee`. The header ends in a blank cell over the scroll bar,
  one black line between them.
- A row is 18px and a white line. Its icon is the 32px art at 16, 22px
  into the Name column; its name starts a pixel past the icon; a
  column's text is 6px in and its header's 8px. Size is set right. Dates
  read "Today, 3:03 AM", "Yesterday, 11:40 PM", "Sun, Jun 1, 2003, 12:02
  PM"; a folder's size is a dash and an empty file's "zero K".
- A folder's row has the Finder's disclosure triangle in the 15px before
  the icon (`FL_SHUT`, `FL_OPEN`: the sampled pixels, 11 tall pointing
  right, 10 wide pointing down, `#ccccff` `#9999ff` `#6666cc` in a black
  outline with an `#888` and `#bbb` shadow that is part of the drawing).
  Pressed, its lavender is `#333366` and its shadow `#444` and `#555`.
  The flip between the two has no frame in between. The triangle is 7px
  into the Name column at every depth: an open folder's items are listed
  under it with icon and name 24px further in a level, and a folder among
  them keeps its triangle in the same gutter. Each folder's items are
  sorted among themselves. The status line counts every row shown, as
  the Finder's does. The gutter's 16px, the row's height, takes the click;
  it does not select. Right and Left open and shut the selected folder
  (the Finder's Command-arrows, which a browser keeps for its history).
- An item is its icon and its name. The rest of its row is the window's
  ground: a click there lets the selection go and a right-click gives the
  window's menu, so a full list can still be asked for its view.
- Name takes the room the other columns leave, down to 180px (the
  Finder's is a fixed 214px, and a column cannot be dragged wider here).
  Those 180px leave a name two levels in some 85px; from the third level
  on the least grows by 24px a level (the Finder widens Name by every
  level, which here would scroll a window of the first size sideways as
  soon as one folder opened).
  The other three are as wide as the widest thing each can say in the
  desk's type, measured once (`flCols`), so no column moves with what a
  folder holds. A window too narrow scrolls sideways, header and all.
- The box that holds the icons keeps its size as a list: the header sits
  in it and the scrolling moves to `.fl-body`, so switching views never
  resizes the window, and the saved layout's height means the same thing
  in both.
- A click on a header sorts by it, in the Finder's first order: newest
  first, largest first with folders last, kinds from A. There is no
  sort-order button and no pressed state.
- A folder's view, its sort and the folders left open in it are its own,
  kept by path in the System app data's `views.json` and applied before
  the folder's first drawing (`viewsReady`), so a list never opens as
  icons and turns over. Only what differs from icons, by name, all shut,
  is written. A folder shut keeps the record of the open ones inside it;
  one that is gone is dropped at the next save. A redrawn list keeps its
  scroll position.
  `~/tools/playwright/exe-finder-views-test.js` holds all of this, on a
  scratch daemon.

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
- A file kind's icon is the document's page (`DOC_ICON`: the outline and
  the folded corner) with one thing drawn on it, in columns 9 to 22 under
  the fold: a landscape, a film strip, lines of text, a disc, a number
  sign. Two change the page itself: the zip's zipper runs its whole
  height, and the classic Mac archive's vise pinches its waist. `fileIcon`
  picks by extension, and each kind registers in `ICON_DEFS`.
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
  editor. The app-list API supplies a trusted `system_icon` SVG only for
  embedded bundles. The desktop registers it as `app-<name>` with get/set,
  keeps its factory art, and uses the registry on the desktop and in Windows
  lists. Leave `<title>` out of these decorative SVGs: inline titles create
  browser tooltips that override the surrounding app label. Disk bundles
  cannot supply inline art through this field, including
  when they override a built-in app's name.

## Apps

- One `index.html`, vanilla JS, inline CSS, no frameworks or CDNs; the
  bundle folder is the app's identity; `app.json` sizes the window.
- An app that reads without the daemon says `"offline"` in its app.json
  (`ui/sw.js`, "what an app keeps", is the contract; Projects the worked
  example). While it shows a kept copy its status line says so and how old
  the copy is, "Offline: the copy of 5 min ago", and whatever would write
  is dimmed where it stands: nothing is hidden and nothing moves. It asks
  every few seconds whether the daemon is back, and is whole again by
  itself. The alert opens it in a new tab, beside itself; in an installed
  app, which has no tabs, in its own place (`?from=offline`), and then a
  phone's first screen has Back to exe in its bar.
- An app window follows its app's code: `/v1/apps` lists each app's
  `version` (a fingerprint of its files) and the daemon announces a change
  on the app-data stream as `{app: "@apps", path, version}`; the desk
  reloads a window left on an older version once nobody is using it (at
  once in a hidden tab, 3 quiet seconds for a closed window, 30 for one on
  screen, never over a typed draft or an open menu), on its latest args, and
  tells a closed one `hide` again. An app needs nothing new for it beyond
  the contract: flush on pagehide, and mark a field it saves as typed
  `data-autosave` (a field without it holds the reload while it has text).
- The desktop bridge: `{exe:"focus"}` on pointerdown, `grow-start / grow /
  grow-end`, `hide` and `show` (pause loops and timers), `data-changed`,
  `workspace-changed` (a Workspace file moved: `path`, `deleted`, `client`).
  An app opens another with `{exe:"open-app", app, path}`: the desk opens
  that app's window with `?path=` on a first open, or posts `{exe:"open",
  path}` into the window already there (Planet hands a post to the Writer).
  `{exe:"view-image", url, name}` opens a picture in a PictureViewer window
  of the desk's (a hub embed, a Projects attachment): `url` is the whole
  http(s) address, fetched by the desk itself, and `name` the title.
  `{exe:"title", title}` names the app's window for what it shows, after
  the app's own name — "Projects — EXE-12" while that issue is open — and
  an empty `title` is the app's name alone again; the Window menu and Show
  All Windows read the title bar, so they say it too.
  `{exe:"close"}` puts the app's own window away (Welcome's OK). A system
  app — one embedded in the binary, never a disk bundle — may also ask for
  something of the desktop's with `{exe:"desk", act, args}`: `act` is one
  of the desktop menu's action names that open a window (`newvm`,
  `winconfig`, `cfwizard`, `docs`, `app`…, the `BRIDGE_ACTS` set), `args`
  its strings, and an action the menu would show greyed does nothing.
  `winconfig` takes a pane's name and `docs` a chapter's heading there
  (Welcome's Read More…; the same Go test holds each card to a chapter
  that exists).
- An app that is opened from a menu and not from the desktop says
  `"hidden": true` in app.json (Welcome, under Help): it is listed, so its
  window, its icon in the window lists and the Icon Editor all work, but
  it has no desktop icon and is left out of the menu's `@apps`.
- State goes through `/v1/apps/<Name>/data/<file>` under the sync contract
  in `/www/exe-apps/CLAUDE.md`: debounced serialized whole-document PUT
  with `X-Exe-Seq` and `X-Exe-Client`, keepalive flush on pagehide, a
  loaded guard, ids and updated stamps and tombstones on records.
- Check every change in headless Chromium at 1x, 1.5x and 2x, and look at
  the screenshot. Where a real OS 9 sample exists, diff against it.
