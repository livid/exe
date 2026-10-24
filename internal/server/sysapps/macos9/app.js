import RFB from './core/rfb.js';
import MacAudio from './audio.js';
import initCD from './cd.js';
const $ = s => document.querySelector(s);
// This script runs both Macs. The page says which one it shows: the API its
// Mac answers on and the few words that differ (<body data-…>).
const mac = document.body.dataset;
const token = new URLSearchParams(location.search).get('token') || '';
let status, rfb, pending = false, connected = false, nextConnect = 0;
let errorSource = '', apiOffline = false, connectTimer;
const audio = new MacAudio();
const cd = initCD({api: mac.api + '/cd', maxGiB: +mac.cdGib, token, modal:(open, restore) => dialog(open, '#cd-veil', '#cd', '#cd-close', restore)});
let soundWanted = false, soundError = '', soundAttempt = 0;
function updateSound() {
  const available = connected && !!rfb?.capabilities.audio;
  $('#sound').hidden = !status?.running;
  $('#sound').disabled = !available;
  $('#sound').classList.toggle('on', audio.enabled);
  $('#sound').setAttribute('aria-pressed', String(audio.enabled));
  $('#sound').setAttribute('aria-label', audio.enabled ? 'Mute sound' : 'Enable sound');
  $('#sound-label').textContent = audio.enabled ? 'Sound on' : 'Sound off';
  $('#sound').title = soundError || (!available ? 'Sound is unavailable on this Mac.' : audio.enabled ? 'Mute sound' : 'Enable sound');
}
function stopSound() {
  ++soundAttempt;
  rfb?.setAudioEnabled(false); audio.stop(); updateSound();
}
async function startSound() {
  const client = rfb, attempt = ++soundAttempt;
  if (!connected || !client?.capabilities.audio || !viewActive || document.hidden) return;
  try {
    const started = await audio.start();
    if (attempt !== soundAttempt) return;
    if (started && rfb === client && connected && viewActive && !document.hidden) {
      client.setAudioEnabled(true); soundError = '';
    } else audio.stop();
  } catch (e) { if (attempt === soundAttempt) { soundError = e.message; audio.stop(); } }
  if (attempt === soundAttempt) updateSound();
}
$('#sound').onclick = () => {
  soundError = '';
  if (audio.enabled) { soundWanted = false; stopSound(); }
  else { soundWanted = true; startSound(); }
};
async function api(path = '', method = 'GET', body) {
  const controller = new AbortController();
  // A half-open connection must not prevent every later status poll.
  const timeout = setTimeout(() => controller.abort(), method === 'GET' ? 8000 : 30000);
  try {
    const headers = token ? {Authorization: 'Bearer ' + token} : {};
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    const response = await fetch(mac.api + path, {method, signal: controller.signal, headers, cache: 'no-store',
      body: body === undefined ? undefined : JSON.stringify(body)});
    if (!response.ok) {
      const e = new Error(response.status === 401 ? 'Set your API token in exe’s Special menu, then reopen this app.' : (await response.text()).trim() || `HTTP ${response.status}`);
      // A failed action (including HTTP 500) needs attention. Status polling
      // and temporary gateway failures can recover through reconnection.
      e.retryable = (method === 'GET' && response.status >= 500) || [408, 429, 502, 503, 504].includes(response.status);
      throw e;
    }
    return await response.json();
  } catch (e) {
    if (controller.signal.aborted || e instanceof TypeError) e.retryable = true;
    throw e;
  } finally { clearTimeout(timeout); }
}
function clearError(source) {
  if (source && errorSource !== source) return;
  $('#error').hidden = true; $('#error').textContent = ''; errorSource = '';
}
function reconnecting() {
  apiOffline = true;
  clearError('request');
  $('#message').textContent = 'Waiting for exe to reconnect…';
  $('#message').title = 'This window will reconnect automatically.';
  $('#connection').textContent = 'Reconnecting…';
  $('#lamp').classList.remove('on');
}
function error(e, source = 'action') {
  if (e.retryable) { reconnecting(); return; }
  errorSource = source; $('#error').textContent = e.message; $('#error').hidden = false;
}
// The bar's words give way to glyphs when they would not fit in one row. The
// row is measured, not a width named in the stylesheet: the type is the
// visitor's own, and the bar must never be taller in a wider window than in a
// narrower one, because the window is fitted around this page's height
// (fitWindow) and two such widths chase each other for as long as it stands.
const bar = $('#bar');
function fitBar() {
  bar.classList.remove('glyphs');
  const tops = [...bar.children].filter(el => el.getClientRects().length).map(el => el.offsetTop);
  bar.classList.toggle('glyphs', Math.max(...tops) > Math.min(...tops) + 16);
}
new ResizeObserver(fitBar).observe(document.documentElement);
// Whole device pixels keep the guest's bitmap text even at fractional OS
// scaling. Give noVNC the matching CSS viewport so input uses the same scale.
const screen = $('#screen'), display = $('#display');
// iPad Safari before 16.4 exposes only the prefixed fullscreen API.
const requestFullscreen = screen.requestFullscreen || screen.webkitRequestFullscreen;
const fullscreenElement = () => document.fullscreenElement || document.webkitFullscreenElement;
let expandedDisplay = false;
let lastWindowSize = '';
function fitWindow(canvas) {
  if (parent === window || expandedDisplay || fullscreenElement()) return;
  // Keep the toolbar, setup guide and status bar at their normal UI size.
  // Their heights are fractions of a CSS pixel at fractional display scaling
  // (a 1px border is one device pixel), and a long guide wraps to any height:
  // sum what is laid out, not each offsetHeight's rounding, and round the
  // total up, or the screen comes out a fraction short of a whole pixel scale.
  const chromeHeight = Math.ceil(['#bar', '#status', '#guide', '#error'].reduce((sum, selector) => {
    const el = $(selector), css = getComputedStyle(el);
    return sum + (el.hidden ? 0 : el.getBoundingClientRect().height + parseFloat(css.marginTop || 0) + parseFloat(css.marginBottom || 0));
  }, parseFloat(getComputedStyle($('main')).borderTopWidth) || 0) - 1e-6);
  // With the setup guide open the window keeps a width the guide reads at.
  // Fitted to a display that had to shrink, a narrower window made the guide
  // longer, which shrank the display again: down to a Mac 200 pixels wide.
  const guide = $('#guide'), minWidth = !guide.hidden && guide.open ? 640 : 0;
  const key = [canvas.width, canvas.height, chromeHeight, minWidth, screen.clientWidth, screen.clientHeight, devicePixelRatio].join(':');
  if (key === lastWindowSize) return;
  lastWindowSize = key;
  parent.postMessage({exe: 'display-size', width: canvas.width, height: canvas.height, chromeHeight, minWidth}, location.origin);
}
function fitDisplay() {
  const canvas = display.querySelector('canvas');
  if (!canvas?.width || !canvas.height) return;
  const bounds = screen.getBoundingClientRect();
  const {width, height} = bounds;
  if (!width || !height) return;
  const density = window.devicePixelRatio || 1;
  const fit = Math.min(width / canvas.width, height / canvas.height);
  const pixels = Math.floor(fit * density + 1e-6);
  const scale = pixels >= 1 ? pixels / density : fit;
  const w = canvas.width * scale, h = canvas.height * scale;
  // Include the iframe's position when centering on the physical pixel grid.
  const frame = window.frameElement?.getBoundingClientRect();
  const x = bounds.left + (frame?.left || 0), y = bounds.top + (frame?.top || 0);
  display.style.width = w + 'px';
  display.style.height = h + 'px';
  display.style.left = (Math.round((x + (width - w) / 2) * density) / density - x) + 'px';
  display.style.top = (Math.round((y + (height - h) / 2) * density) / density - y) + 'px';
  screen.classList.toggle('integer-scale', pixels >= 1);
  fitWindow(canvas);
}
// A move between monitors or browser zoom can change density without changing
// this iframe's CSS dimensions. Rearm the query for each new density.
let densityQuery, displayDensity;
function densityChanged() {
  densityQuery?.removeEventListener('change', densityChanged);
  displayDensity = window.devicePixelRatio || 1;
  densityQuery = matchMedia(`(resolution: ${displayDensity}dppx)`);
  densityQuery.addEventListener('change', densityChanged);
  lastWindowSize = ''; fitDisplay();
}
densityChanged();
const displayResize = new ResizeObserver(fitDisplay);
displayResize.observe(screen);
// Refit on connection and when the guest changes its framebuffer resolution.
const framebufferResize = new MutationObserver(fitDisplay);
framebufferResize.observe(display, {childList: true, subtree: true, attributes: true, attributeFilter: ['width', 'height']});
// The arrow is where the browser's pointer is. Mac OS X's HID driver, from
// the updates of Tiger on (10.4.11), leaves 7.5% of a tablet's range unused
// at each edge, where 10.4.0 used all of it, and nothing in the guest can be
// set about it: uncorrected, the arrow stands off from the pointer, the more
// the nearer an edge. The viewer sends each place corrected (pointerTrim in
// core/rfb.js) and finds out by looking which of the system's corrections
// (<body data-pointer-trims>) the guest in front of it needs. With the
// pointer at rest it reads the Mac's arrow, a shape it knows, off the screen
// it is shown. Found under the pointer, the correction in use is right;
// found three rests running where another correction would have put it,
// that one is taken, and the daemon is told, so that every viewer of this
// Mac begins with it. A page that names no corrections (Mac OS 9's) does
// none of this.
const trims = (mac.pointerTrims || '').split(' ').filter(Boolean).map(Number);
const ARROW = [ // the Mac's arrow, from the row above its tip and the column left of it
  'WW', 'WBW', 'WBBW', 'WBBBW', 'WBBBBW', 'WBBBBBW', 'WBBBBBBW', 'WBBBBBBBW', 'WBBBBBBBBW', 'WBBBBBWWWWW',
  'WBBWBBW', 'WBW WBBW', 'WW  WBBW', 'W    WBBW', '     WBBW', '      WWW'];
let trim = 0, trimVotes = 0, trimVoted, restTimer;
function setTrim(value, tell) {
  if (!trims.includes(value) || value === trim) return;
  trim = value; trimVotes = 0;
  if (rfb) rfb.pointerTrim = trim;
  if (tell) fetch(mac.api + '/pointer', {method: 'POST', cache: 'no-store', body: JSON.stringify({trim}),
    headers: {'Content-Type': 'application/json', ...(token ? {Authorization: 'Bearer ' + token} : {})}}).catch(() => {});
}
function arrowAt(ctx, x, y) {
  if (x < 1 || y < 1 || x + 10 > ctx.canvas.width || y + 15 > ctx.canvas.height) return false;
  const px = ctx.getImageData(x - 1, y - 1, 11, 16).data;
  let wrong = 0;
  for (let r = 0; r < ARROW.length; r++) for (let c = 0; c < ARROW[r].length; c++) {
    const want = ARROW[r][c]; if (want === ' ') continue;
    const i = (r * 11 + c) * 4, lo = Math.min(px[i], px[i + 1], px[i + 2]), hi = Math.max(px[i], px[i + 1], px[i + 2]);
    if ((want === 'B' ? hi > 80 : lo < 176) && ++wrong > 3) return false;
  }
  return true;
}
// the guest pixel a place sent lands on, in a guest that leaves `unused` of the range unused
const landsOn = (sent, size, unused) => Math.floor((Math.floor(sent * 32767 / size) - unused / 2 * 32767) * size / ((1 - unused) * 32767));
function lookForArrow() {
  const sent = rfb?.pointerSent, canvas = display.querySelector('canvas');
  if (!connected || !sent || !canvas?.width) return;
  const ctx = canvas.getContext('2d');
  const found = unused => {
    const x = landsOn(sent.sentX, canvas.width, unused), y = landsOn(sent.sentY, canvas.height, unused);
    for (let dy = -1; dy <= 1; dy++) for (let dx = -1; dx <= 1; dx++) if (arrowAt(ctx, x + dx, y + dy)) return true;
    return false;
  };
  if (found(trim)) { trimVotes = 0; return; }
  const other = trims.find(t => t !== trim && found(t));
  if (other === undefined) return;
  if (other !== trimVoted) { trimVoted = other; trimVotes = 0; }
  if (++trimVotes >= 3) setTrim(other, true);
}
if (trims.length > 1) display.addEventListener('pointermove', e => {
  clearTimeout(restTimer);
  if (!e.buttons) restTimer = setTimeout(lookForArrow, 350);
}, true);
function connect() {
  if (!viewActive || rfb || Date.now() < nextConnect) return;
  const url = new URL(mac.api + '/console', location.href);
  url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
  if (token) url.searchParams.set('token', token);
  const client = new RFB(display, url.href, {wsProtocols: ['binary']});
  rfb = client; client.scaleViewport = true; client.resizeSession = false; client.background = '#333';
  client.pointerTrim = trim;
  // three update requests in flight: exe's QEMU answers each, so frames keep coming while
  // the last one crosses the network (core/rfb.js framesInFlight)
  client.framesInFlight = 3;
  client.addEventListener('capabilities', () => {
    if (rfb !== client) return;
    updateSound();
    if (soundWanted && client.capabilities.audio && !audio.enabled) startSound();
  });
  client.addEventListener('audio', e => {
    if (rfb !== client) return;
    if (e.detail.type === 'data') audio.push(e.detail.data);
    else audio.reset();
  });
  $('#connection').textContent = 'Connecting…';
  connectTimer = setTimeout(() => { if (rfb === client && !connected) client.disconnect(); }, 10000);
  client.addEventListener('connect', () => {
    if (rfb !== client) return;
    clearTimeout(connectTimer); clearError('display');
    $('#mac-keys').disabled = shortcutBusy;
    connected = true; $('#connection').textContent = 'Connected'; $('#lamp').classList.add('on');
    $('#mac-keys-popup').hidden = false; $('#full').hidden = false;
    updateSound();
    if (soundWanted && client.capabilities.audio) startSound();
  });
  client.addEventListener('disconnect', () => {
    if (rfb !== client) return;
    clearTimeout(connectTimer);
    connected = false; rfb = null;
    stopSound();
    nextConnect = Date.now() + 3000;
    $('#connection').textContent = status?.running || apiOffline ? 'Reconnecting…' : '';
    $('#lamp').classList.remove('on'); $('#mac-keys').disabled = true;
  });
  client.addEventListener('securityfailure', e => {
    if (rfb === client) error(new Error(e.detail.reason || 'The Mac display connection was refused.'), 'display');
  });
}
// A first setup begins as the app opens, unless the page says what it will
// fetch and waits to be asked (data-before-setup): Mac OS X's begins with a
// download of gigabytes, which a look at the app should not start.
const asks = s => s?.phase === 'new' && !s.active && !!mac.beforeSetup;
function render(s) {
  const initialSetup = !status && !s.installed && !s.running && s.phase !== 'installing' && !asks(s);
  status = s;
  if (!s.running && expandedDisplay) expandDisplay(false);
  $('#message').textContent = s.message;
  $('#message').title = s.message;
  $('#hardware').textContent = mac.hardware + ' · ' + (s.installed ? 'Ethernet NAT' : 'Ethernet after installation');
  // Where this computer reaches the Mac's own Sharing services, on a page that says so (Mac OS X's).
  for (const el of document.querySelectorAll('[id^="port-"]')) el.textContent = s.ports?.[el.id.slice(5)] || '—';
  renderAgent(s);
  $('#start').hidden = s.active || s.running;
  $('#start').textContent = s.installed ? 'Start Mac' : s.phase === 'installing' ? 'Resume installer' : asks(s) ? 'Set up Mac' : 'Continue setup';
  $('#start').disabled = pending;
  $('#cancel').hidden = !s.active; $('#cancel').disabled = pending;
  $('#setup-loading').hidden = true;
  $('#steps').replaceChildren(...s.steps.map((step, i) => {
    const li = document.createElement('li'); li.className = step.state;
    const badge = document.createElement('span'); badge.className = 'badge';
    badge.textContent = step.state === 'done' ? '✓' : String(i + 1);
    const text = document.createElement('div'), title = document.createElement('strong'), detail = document.createElement('small');
    title.textContent = step.title; detail.textContent = step.detail || ({pending: 'Waiting', working: 'In progress', waiting: 'Your turn', error: 'Needs attention'}[step.state] || 'Complete');
    text.append(title, detail); li.append(badge, text); return li;
  }));
  const downloading = s.active && s.total > 0 && s.steps[1].state === 'working';
  $('#download-progress').hidden = !downloading; $('#progress').max = s.total || 1; $('#progress').value = s.bytes;
  $('#transfer').textContent = downloading ? `${(s.bytes / 1048576).toFixed(1)} / ${(s.total / 1048576).toFixed(1)} MiB` : '';
  $('#guide').hidden = s.installed || (!s.running && s.phase !== 'installing');
  $('#finish').disabled = s.running || s.active || pending;
  $('#screen').hidden = !s.running;
  $('#bar-help').hidden = !s.running;
  $('#mac-keys-popup').hidden = !s.running; $('#full').hidden = !s.running;
  $('#mac-keys').disabled = !connected || shortcutBusy;
  updateSound();
  cd.update(s.running);
  $('#cd-group').hidden = !s.running;
  $('#empty').hidden = s.running;
  $('#empty-text').textContent = s.active ? 'Your Mac is being set up. Open Setup details to follow its progress.' :
    s.phase === 'installing' ? mac.afterInstall :
    s.installed ? 'Your Mac is shut down. Its files are saved. Click Start Mac to return to the desktop.' :
    asks(s) ? mac.beforeSetup :
    'Open Setup details to review progress, or choose Continue setup to resume.';
  if (s.phase === 'error' || s.phase === 'interrupted') error(new Error(s.message), 'setup');
  else clearError('setup');
  fitBar();
  if (typeof s.trim === 'number' || trims.length) setTrim(s.trim || 0);
  if (s.running) connect();
  else if (rfb) { rfb.disconnect(); }
  else $('#connection').textContent = '';
  if (initialSetup) setupDetails(true);
}
// The agent's way in (a Mac with a shell: Mac OS X's page has the controls,
// Mac OS 9's has none): the Agent button opens a Chat pinned to the Mac
// through the desk, and Setup details takes the Mac account to work as and
// shows the line that authorizes exe's key there. The account field keeps
// what is being typed through the polls.
function renderAgent(s) {
  const btn = $('#agent'), box = $('#agent-setup');
  if (!btn || !box) return;
  btn.hidden = !(s.running && s.installed && s.agent);
  box.hidden = !s.agent;
  const item = $('#setup-list [data-page="agent"]');
  if (item) { item.classList.toggle('off', !s.agent); item.setAttribute('aria-disabled', String(!s.agent)); }
  if (!s.agent) return;
  const user = $('#agent-user');
  if (document.activeElement !== user && !agentSaving) user.value = s.agent.user || '';
  $('#agent-line').value = authorizeLine(s.agent.key);
}
const authorizeLine = key => key ? `mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '${key}' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys` : '';
let agentSaving = false;
async function saveAgentUser() {
  const user = $('#agent-user').value.trim();
  if (!status?.agent || user === (status.agent.user || '')) return;
  agentSaving = true; clearError();
  try { render(await api('/agent', 'PUT', {user})); } catch (e) { error(e); }
  finally { agentSaving = false; }
}
$('#agent')?.addEventListener('click', () => parent.postMessage({exe: 'chat'}, location.origin));
$('#agent-user')?.addEventListener('change', saveAgentUser);
$('#agent-user')?.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); saveAgentUser(); } });
$('#agent-line')?.addEventListener('focus', e => e.target.select());
// The Workspace volume's address and password, on a page that has the
// block (Mac OS X's); New Password renews it and drops every Mac that
// mounted with the old one.
async function renderShare(renew = false) {
  if (!$('#share')) return;
  const btn = $('#share-renew');
  btn.disabled = true;
  try {
    const headers = token ? {Authorization: 'Bearer ' + token} : {};
    const response = await fetch('/v1/dav', {method: renew ? 'POST' : 'GET', headers, cache: 'no-store'});
    if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
    const s = await response.json();
    // Connect from: the Mac in this window at the host's address under
    // QEMU's user networking, then any other computer at this machine's.
    const from = $('#share-from'), was = from.value;
    const choices = [[s.mac, 'The Mac in this window'], ...s.addresses.map(a => [a, 'Another computer, at ' + new URL(a).hostname])];
    if (choices.length !== from.options.length || choices.some(([v], i) => from.options[i].value !== v)) {
      from.replaceChildren(...choices.map(([v, t]) => new Option(t, v)));
      from.value = choices.some(([v]) => v === was) ? was : choices[0][0];
    }
    $('#share-address').value = from.value;
    $('#share-password').value = s.password;
  } catch (e) { error(e); }
  finally { btn.disabled = false; }
}
$('#share-renew')?.addEventListener('click', () => renderShare(true));
$('#share-from')?.addEventListener('change', e => { $('#share-address').value = e.target.value; });
// Copy beside a field selects its text and puts it on the clipboard, and
// reads Copied for a moment. A plain-http desk (the Tailscale address) has
// no clipboard API; there the copy command does it on the selection.
$('#setup')?.addEventListener('click', async e => {
  const b = e.target instanceof Element && e.target.closest('.copy');
  if (!b) return;
  const input = $('#' + b.dataset.copy);
  input.focus({preventScroll: true}); input.select();
  let ok = false;
  try { await navigator.clipboard.writeText(input.value); ok = true; }
  catch { try { ok = document.execCommand('copy'); } catch {} }
  if (!ok) return;
  b.textContent = 'Copied'; clearTimeout(b._t);
  b._t = setTimeout(() => { b.textContent = 'Copy'; }, 1500);
});
renderShare();
async function action(path) {
  if (pending) return;
  pending = true; clearError();
  try { render(await api(path, 'POST')); } catch (e) { error(e); }
  finally { pending = false; if (status) render(status); }
}
$('#start').onclick = async () => {
  const first = asks(status);
  await action('/start');
  if (first && status?.active) setupDetails(true);
};
$('#cancel').onclick = () => action('/cancel');
$('#finish').onclick = async () => {
  if (pending) return;
  pending = true; clearError();
  try { await api('/finish', 'POST'); render(await api('/start', 'POST')); } catch (e) { error(e); }
  finally { pending = false; if (status) render(status); }
};
function dialog(open, veilID, triggerID, defaultID, restoreFocus = true) {
  const veil = $(veilID);
  if (veil.classList.contains('on') === open) return;
  veil.classList.toggle('on', open);
  if ($(triggerID).getAttribute('aria-haspopup') === 'dialog') $(triggerID).setAttribute('aria-expanded', String(open));
  for (const el of [$('#bar'), $('main'), $('#error'), $('#status')]) el.inert = open;
  if (open) { rfb?.blur(); $(defaultID).focus({preventScroll: true}); }
  else if (restoreFocus) $(triggerID).focus({preventScroll: true});
}
// The dialog's pages, chosen from the bar's Setup menu on Mac OS X's page:
// the setup steps (all that Mac OS 9's Setup details button opens), the
// Workspace volume, and the agent's way in. Setup opens the steps by
// itself; a dialog already open keeps its page through the polls.
const setupPages = {details: 'Setup details', share: 'Mount Workspace', agent: 'Agent setup'};
const setupTrigger = $('#setup-menu') ? '#setup-menu' : '#details';
function setupDetails(open, restoreFocus = true, page = 'details') {
  const dlg = $('#dlg');
  if (open && 'page' in dlg.dataset && !$('#veil').classList.contains('on')) {
    dlg.dataset.page = page;
    $('#setup-title').textContent = setupPages[page];
    if (page === 'share') renderShare();
  }
  dialog(open, '#veil', setupTrigger, '#setup-close', restoreFocus);
}
$('#details')?.addEventListener('click', () => setupDetails(true));
// The Setup menu's list hangs 2px under the button at its left edge, at
// least as wide, kept inside the window. The row under the pointer or
// the arrow keys is .on; Return or a click picks; Escape, a press
// elsewhere or the window losing the keyboard puts the list away.
const setupBtn = $('#setup-menu'), setupList = $('#setup-list');
function setupRows() { return [...setupList.children].filter(r => !r.classList.contains('off')); }
function setupOn(row) { for (const r of setupList.children) r.classList.toggle('on', r === row); }
function openSetupMenu() {
  setupList.hidden = false;
  const r = $('#setup-popup').getBoundingClientRect();
  setupList.style.minWidth = r.width + 'px';
  setupList.style.top = (r.bottom + 2) + 'px';
  setupList.style.maxHeight = Math.max(72, innerHeight - r.bottom - 10) + 'px';
  // a whole number of pixels wide, so its right border is one device pixel
  setupList.style.width = '';
  setupList.style.width = Math.ceil(setupList.getBoundingClientRect().width) + 'px';
  setupList.style.left = Math.max(4, Math.min(r.left, innerWidth - setupList.offsetWidth - 6)) + 'px';
  setupOn(null);
  setupBtn.setAttribute('aria-expanded', 'true');
}
function closeSetupMenu() {
  if (setupList.hidden) return;
  setupList.hidden = true;
  setupBtn.setAttribute('aria-expanded', 'false');
}
function pickSetup(row) {
  if (!row || row.classList.contains('off')) return;
  closeSetupMenu();
  setupDetails(true, true, row.dataset.page);
}
if (setupBtn && setupList) {
  setupBtn.addEventListener('click', () => setupList.hidden ? openSetupMenu() : closeSetupMenu());
  setupList.addEventListener('pointermove', e => {
    const row = e.target.closest('.dd-item');
    if (row && !row.classList.contains('on') && !row.classList.contains('off')) setupOn(row);
  });
  setupList.addEventListener('click', e => pickSetup(e.target.closest('.dd-item')));
  document.addEventListener('pointerdown', e => {
    if (!e.target.closest('#setup-list, #setup-popup')) closeSetupMenu();
  }, true);
  addEventListener('blur', closeSetupMenu);
  document.addEventListener('keydown', e => {
    if (setupList.hidden) {
      if (document.activeElement === setupBtn && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) { e.preventDefault(); openSetupMenu(); }
      return;
    }
    const rows = setupRows(), at = rows.findIndex(r => r.classList.contains('on'));
    if (e.key === 'Escape') closeSetupMenu();
    else if (e.key === 'ArrowDown') setupOn(rows[Math.min(rows.length - 1, at + 1)]);
    else if (e.key === 'ArrowUp') setupOn(rows[Math.max(0, at - 1)]);
    else if (e.key === 'Enter' && at >= 0) pickSetup(rows[at]);
    else return;
    e.preventDefault();
  });
}
$('#setup-close').onclick = () => setupDetails(false);
document.addEventListener('keydown', e => {
  const panel = $('#cd-veil').classList.contains('on') ? $('#cd-dlg') : $('#veil').classList.contains('on') ? $('#dlg') : null;
  if (!panel) return;
  e.stopImmediatePropagation();
  if (e.key === 'Escape' || (e.key === 'Enter' && !e.target.closest('a, button, select, input'))) {
    e.preventDefault(); panel.id === 'cd-dlg' ? cd.close(true) : setupDetails(false);
  } else if (e.key === 'Tab') {
    const items = [...panel.querySelectorAll('a[href], button:not(:disabled), select:not(:disabled), input:not(:disabled), textarea, [tabindex="0"]')].filter(el => el.getClientRects().length);
    const first = items[0], last = items[items.length - 1];
    if (!panel.contains(document.activeElement)) { e.preventDefault(); (e.shiftKey ? last : first).focus(); }
    else if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }
}, true);
let shortcutBusy = false;
$('#mac-keys').onchange = async e => {
  const key = e.target.value; e.target.value = '';
  const client = rfb; if (!connected || !key || shortcutBusy) return;
  shortcutBusy = true; e.target.disabled = true;
  // A shortcut is its keys by name, joined with "+": the modifiers below, or
  // a letter. A Shift tap alone wakes Energy Saver without typing or running
  // a command; USB tablet movement alone does not wake this guest.
  const named = {cmd: [0xffeb, 'MetaLeft'], opt: [0xffe9, 'AltLeft'], shift: [0xffe1, 'ShiftLeft'],
    esc: [0xff1b, 'Escape'], tab: [0xff09, 'Tab'], space: [0x20, 'Space']};
  const keys = key.split('+').map(k => named[k] || [k.charCodeAt(0), 'Key' + k.toUpperCase()]);
  // Classic Mac OS polls keyboard state; keep each key down long enough.
  for (const [symbol, code] of keys) {
    client.sendKey(symbol, code, true);
    await new Promise(resolve => setTimeout(resolve, 300));
  }
  for (const [symbol, code] of [...keys].reverse()) client.sendKey(symbol, code, false);
  client.focus();
  shortcutBusy = false; e.target.disabled = !connected;
};
function fullscreenChanged() {
  lastWindowSize = ''; fitDisplay();
  if (fullscreenElement()) { clearError('fullscreen'); rfb?.focus(); }
}
// Home Screen web apps can have no fullscreen API. Expand the existing iframe
// within exe instead, keeping a visible exit control and the same VNC session.
function expandDisplay(active) {
  if (expandedDisplay === active) return;
  expandedDisplay = active;
  document.body.classList.toggle('expanded-display', active);
  $('#full').textContent = active ? 'Exit full screen' : 'Full screen';
  $('#full').setAttribute('aria-pressed', String(active));
  parent.postMessage({exe: 'display-fullscreen', active}, location.origin);
  clearError('fullscreen'); lastWindowSize = ''; fitDisplay();
  if (active) rfb?.focus();
  else $('#full').focus();
}
function fullscreenError() {
  if (viewActive && status?.running) expandDisplay(true);
}
for (const event of ['fullscreenchange', 'webkitfullscreenchange']) document.addEventListener(event, fullscreenChanged);
for (const event of ['fullscreenerror', 'webkitfullscreenerror']) document.addEventListener(event, fullscreenError);
$('#full').onclick = async () => {
  if (expandedDisplay) { expandDisplay(false); return; }
  if (typeof requestFullscreen !== 'function') { expandDisplay(true); return; }
  clearError('fullscreen');
  // Invoke during the click: both APIs require user activation. Older Safari
  // returns void and reports completion or refusal through prefixed events.
  try { await requestFullscreen.call(screen); } catch (e) { fullscreenError(); }
};
document.addEventListener('keydown', e => {
  if (expandedDisplay && e.key === 'Escape') { e.preventDefault(); e.stopImmediatePropagation(); expandDisplay(false); }
}, true);
// Raise the app before noVNC consumes the pointer event on its canvas.
document.addEventListener('pointerdown', () => parent.postMessage({exe:'focus'}, location.origin), true);
// OS 9 scrollbar end-merges need the same scrolled/at-end flags as the desktop
document.addEventListener("scroll", e => {
  const el = e.target;
  if (!(el instanceof Element)) return;
  el.classList.toggle("scrolled-y", el.scrollTop >= 1);
  el.classList.toggle("at-y-end", el.scrollTop >= 1 && el.scrollTop >= el.scrollHeight - el.clientHeight - 1);
}, { capture: true, passive: true });

let initial = true, viewActive = true, polling = false, pollTimer;
async function poll() {
  if (!viewActive || polling) return;
  // Some embedded browsers change density without a media-query event.
  // Reuse the status heartbeat as a fallback; no extra polling timer.
  if (displayDensity !== (window.devicePixelRatio || 1)) densityChanged();
  polling = true;
  try {
    const s = await api();
    apiOffline = false; clearError('request');
    if (connected) { $('#connection').textContent = 'Connected'; $('#lamp').classList.add('on'); }
    render(s);
    cd.refresh();
    if (initial && !s.running && !asks(s) && (s.phase === 'new' || s.phase === 'stopped')) { initial = false; await action('/start'); }
    initial = false;
  } catch (e) { error(e, 'request'); }
  finally {
    polling = false;
    if (viewActive) pollTimer = setTimeout(poll, 1500);
  }
}
// Browser connectivity can return before the next scheduled retry.
window.addEventListener('online', () => {
  if (!viewActive) return;
  nextConnect = 0; clearTimeout(pollTimer); poll();
});
window.addEventListener('message', e => {
  if (e.origin !== location.origin || e.source !== parent || !e.data) return;
  if (e.data.exe === 'hide') {
    stopSound();
    setupDetails(false, false); cd.close(false);
    expandDisplay(false);
    viewActive = false; clearTimeout(pollTimer); rfb?.disconnect();
  } else if (e.data.exe === 'show') {
    viewActive = true; nextConnect = 0; lastWindowSize = ''; clearTimeout(pollTimer); poll();
  }
});
document.addEventListener('visibilitychange', () => {
  if (document.hidden) stopSound();
  else if (soundWanted) startSound();
});
window.addEventListener('pagehide', () => { stopSound(); setupDetails(false, false); cd.close(false); expandDisplay(false); viewActive = false; clearTimeout(pollTimer); rfb?.disconnect(); });
poll();
