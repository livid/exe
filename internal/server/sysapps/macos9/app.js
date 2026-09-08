import RFB from './core/rfb.js';
const $ = s => document.querySelector(s);
const token = new URLSearchParams(location.search).get('token') || '';
let status, rfb, pending = false, showDetails = null, connected = false, nextConnect = 0;
let errorSource = '', apiOffline = false, connectTimer;
async function api(path = '', method = 'GET') {
  const controller = new AbortController();
  // A half-open connection must not prevent every later status poll.
  const timeout = setTimeout(() => controller.abort(), method === 'GET' ? 8000 : 30000);
  try {
    const response = await fetch('/v1/macos9' + path, {method, signal: controller.signal,
      headers: token ? {Authorization: 'Bearer ' + token} : {}, cache: 'no-store'});
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
// Give noVNC an integer-sized viewport so its own scaling and input mapping
// stay in sync. Below native size, let it shrink proportionally to fit.
const screen = $('#screen'), display = $('#display');
// iPad Safari before 16.4 exposes only the prefixed fullscreen API.
const requestFullscreen = screen.requestFullscreen || screen.webkitRequestFullscreen;
const fullscreenElement = () => document.fullscreenElement || document.webkitFullscreenElement;
let expandedDisplay = false;
let lastWindowSize = '';
function fitWindow(canvas) {
  if (parent === window || expandedDisplay || fullscreenElement()) return;
  // Keep the toolbar, setup guide and status bar at their normal UI size.
  const chromeHeight = 1 + ['#bar', '#status', '#setup', '#guide', '#error'].reduce((sum, selector) => {
    const el = $(selector), css = getComputedStyle(el);
    return sum + (el.hidden ? 0 : el.offsetHeight + parseFloat(css.marginTop || 0) + parseFloat(css.marginBottom || 0));
  }, 0);
  const key = [canvas.width, canvas.height, chromeHeight, screen.clientWidth, screen.clientHeight].join(':');
  if (key === lastWindowSize) return;
  lastWindowSize = key;
  parent.postMessage({exe: 'display-size', width: canvas.width, height: canvas.height, chromeHeight}, location.origin);
}
function fitDisplay() {
  const canvas = display.querySelector('canvas');
  if (!canvas?.width || !canvas.height) return;
  const width = screen.clientWidth, height = screen.clientHeight;
  if (!width || !height) return;
  const fit = Math.min(width / canvas.width, height / canvas.height);
  const scale = Math.floor(fit);
  const w = scale >= 1 ? canvas.width * scale : width;
  const h = scale >= 1 ? canvas.height * scale : height;
  display.style.width = w + 'px';
  display.style.height = h + 'px';
  display.style.left = Math.floor((width - w) / 2) + 'px';
  display.style.top = Math.floor((height - h) / 2) + 'px';
  screen.classList.toggle('integer-scale', scale >= 1);
  fitWindow(canvas);
}
const displayResize = new ResizeObserver(fitDisplay);
displayResize.observe(screen);
// Refit on connection and when the guest changes its framebuffer resolution.
const framebufferResize = new MutationObserver(fitDisplay);
framebufferResize.observe(display, {childList: true, subtree: true, attributes: true, attributeFilter: ['width', 'height']});
function connect() {
  if (!viewActive || rfb || Date.now() < nextConnect) return;
  const url = new URL('/v1/macos9/console', location.href);
  url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
  if (token) url.searchParams.set('token', token);
  const client = new RFB(display, url.href, {wsProtocols: ['binary']});
  rfb = client; client.scaleViewport = true; client.resizeSession = false; client.background = '#333';
  $('#connection').textContent = 'Connecting…';
  connectTimer = setTimeout(() => { if (rfb === client && !connected) client.disconnect(); }, 10000);
  client.addEventListener('connect', () => {
    if (rfb !== client) return;
    clearTimeout(connectTimer); clearError('display');
    $('#mac-keys').disabled = shortcutBusy;
    connected = true; $('#connection').textContent = 'Connected'; $('#lamp').classList.add('on');
    $('#mac-keys-popup').hidden = false; $('#full').hidden = false;
  });
  client.addEventListener('disconnect', () => {
    if (rfb !== client) return;
    clearTimeout(connectTimer);
    connected = false; rfb = null;
    nextConnect = Date.now() + 3000;
    $('#connection').textContent = status?.running || apiOffline ? 'Reconnecting…' : '';
    $('#lamp').classList.remove('on'); $('#mac-keys').disabled = true;
  });
  client.addEventListener('securityfailure', e => {
    if (rfb === client) error(new Error(e.detail.reason || 'The Mac display connection was refused.'), 'display');
  });
}
function render(s) {
  status = s;
  if (!s.running && expandedDisplay) expandDisplay(false);
  $('#message').textContent = s.message;
  $('#message').title = s.message;
  $('#hardware').textContent = 'PowerPC G4 · 512 MB · ' + (s.installed ? 'Ethernet NAT' : 'Ethernet after installation');
  $('#start').hidden = s.active || s.running;
  $('#start').textContent = s.installed ? 'Start Mac' : s.phase === 'installing' ? 'Resume installer' : 'Continue setup';
  $('#start').disabled = pending;
  $('#cancel').hidden = !s.active; $('#cancel').disabled = pending;
  const setupVisible = showDetails ?? (!s.installed && !s.running && s.phase !== 'installing');
  $('#setup').hidden = !setupVisible;
  $('#details').textContent = setupVisible ? 'Hide details' : 'Setup details';
  $('#details').classList.toggle('on', setupVisible);
  $('#details').setAttribute('aria-pressed', String(setupVisible));
  $('#steps').replaceChildren(...s.steps.map((step, i) => {
    const li = document.createElement('li'); li.className = step.state;
    const badge = document.createElement('span'); badge.className = 'badge';
    badge.textContent = step.state === 'done' ? '✓' : String(i + 1);
    const text = document.createElement('div'), title = document.createElement('strong'), detail = document.createElement('small');
    title.textContent = step.title; detail.textContent = step.detail || ({pending: 'Waiting', working: 'In progress', waiting: 'Your turn', error: 'Needs attention'}[step.state] || 'Complete');
    text.append(title, detail); li.append(badge, text); return li;
  }));
  const downloading = s.active && s.total > 0 && s.steps[1].state === 'working';
  $('#progress').hidden = !downloading; $('#progress').max = s.total || 1; $('#progress').value = s.bytes;
  $('#transfer').textContent = downloading ? `${(s.bytes / 1048576).toFixed(1)} / ${(s.total / 1048576).toFixed(1)} MiB` : '';
  $('#guide').hidden = s.installed || (!s.running && s.phase !== 'installing');
  $('#finish').disabled = s.running || s.active || pending;
  $('#screen').hidden = !s.running;
  $('#bar-help').hidden = !s.running;
  $('#mac-keys-popup').hidden = !s.running; $('#full').hidden = !s.running;
  $('#mac-keys').disabled = !connected || shortcutBusy;
  $('#empty').hidden = s.running || s.active || (!s.installed && s.phase !== 'installing');
  $('#empty-text').textContent = s.phase === 'installing' ? 'The Mac has stopped. If Apple Software Restore finished successfully, use the button above to start from your hard disk.' : 'Your Mac is shut down. Its files are saved. Click Start Mac to return to the desktop.';
  if (s.phase === 'error' || s.phase === 'interrupted') error(new Error(s.message), 'setup');
  else clearError('setup');
  if (s.running) connect();
  else if (rfb) { rfb.disconnect(); }
  else $('#connection').textContent = '';
}
async function action(path) {
  if (pending) return;
  pending = true; clearError();
  try { render(await api(path, 'POST')); } catch (e) { error(e); }
  finally { pending = false; if (status) render(status); }
}
$('#start').onclick = () => action('/start');
$('#cancel').onclick = () => action('/cancel');
$('#finish').onclick = async () => {
  if (pending) return;
  pending = true; clearError();
  try { await api('/finish', 'POST'); render(await api('/start', 'POST')); } catch (e) { error(e); }
  finally { pending = false; if (status) render(status); }
};
$('#details').onclick = () => { showDetails = $('#setup').hidden; if (status) render(status); };
let shortcutBusy = false;
$('#mac-keys').onchange = async e => {
  const key = e.target.value; e.target.value = '';
  const client = rfb; if (!connected || !key || shortcutBusy) return;
  shortcutBusy = true; e.target.disabled = true;
  client.sendKey(0xffeb, 'MetaLeft', true);
  // Classic Mac OS polls keyboard state; keep each key down long enough.
  await new Promise(resolve => setTimeout(resolve, 300));
  client.sendKey(key.charCodeAt(0), 'Key' + key.toUpperCase(), true);
  await new Promise(resolve => setTimeout(resolve, 300));
  client.sendKey(key.charCodeAt(0), 'Key' + key.toUpperCase(), false);
  client.sendKey(0xffeb, 'MetaLeft', false);
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
document.addEventListener('pointerdown', () => parent.postMessage({exe:'focus'}, location.origin));
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
  polling = true;
  try {
    const s = await api();
    apiOffline = false; clearError('request');
    if (connected) { $('#connection').textContent = 'Connected'; $('#lamp').classList.add('on'); }
    render(s);
    if (initial && !s.running && (s.phase === 'new' || s.phase === 'stopped')) { initial = false; await action('/start'); }
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
    expandDisplay(false);
    viewActive = false; clearTimeout(pollTimer); rfb?.disconnect();
  } else if (e.data.exe === 'show') {
    viewActive = true; nextConnect = 0; lastWindowSize = ''; clearTimeout(pollTimer); poll();
  }
});
window.addEventListener('pagehide', () => { expandDisplay(false); viewActive = false; clearTimeout(pollTimer); rfb?.disconnect(); });
poll();
