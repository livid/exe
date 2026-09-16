// exe's service worker. The desktop is a live view of the daemon, so nothing
// is ever served from a cache while the daemon answers: API calls, the
// terminals' sockets and app frames are never touched. Its one job is the
// page shown when the daemon does not answer — a top-level load that fails
// on the network, or comes back from a proxy in front of the daemon as a
// 502/503/504, gets /ui/offline.html, precached at install. The daemon
// stamps the version below from the UI build, so a deploy that changes any
// UI byte makes this file differ, which installs a fresh worker and retires
// the old cache.
const VERSION = "__EXE_BUILD__";
const CACHE = "exe-" + VERSION;
const OFFLINE = "/ui/offline.html";

self.addEventListener("install", e => {
  e.waitUntil(caches.open(CACHE)
    .then(c => c.add(new Request(OFFLINE, { cache: "reload" })))
    .then(() => self.skipWaiting()));
});

self.addEventListener("activate", e => {
  e.waitUntil((async () => {
    const keys = await caches.keys();
    await Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)));
    // let the browser start the navigation's request while this worker boots
    if (self.registration.navigationPreload) await self.registration.navigationPreload.enable();
    await self.clients.claim();
  })());
});

self.addEventListener("fetch", e => {
  const r = e.request;
  // only a top-level document load; an app's iframe, a fetch, an image or a
  // socket goes straight to the network as if there were no worker
  if (r.mode !== "navigate" || r.destination !== "document") return;
  e.respondWith((async () => {
    try {
      const res = (await e.preloadResponse) || await fetch(r);
      if (res.status >= 502 && res.status <= 504) throw new Error("gateway " + res.status);
      return res;
    } catch (err) {
      const c = await caches.open(CACHE);
      return (await c.match(OFFLINE)) || Response.error();
    }
  })());
});

// A push from the daemon (the ticker's price alerts, docs/price-alerts.md):
// show it — the tag replaces an older notification for the same token
// rather than stacking — and a tap brings the desktop forward or opens it.
self.addEventListener("push", e => {
  let d = {};
  try { d = e.data ? e.data.json() : {}; } catch (err) { d = { title: "exe", body: e.data ? e.data.text() : "" }; }
  e.waitUntil(self.registration.showNotification(d.title || "exe", {
    body: d.body || "", tag: d.tag || "exe", renotify: true,
    icon: "/ui/icon-192.png", badge: "/ui/icon-192.png", data: { url: d.url || "/" } }));
});

self.addEventListener("notificationclick", e => {
  e.notification.close();
  const url = new URL((e.notification.data && e.notification.data.url) || "/", self.location.origin).href;
  e.waitUntil(self.clients.matchAll({ type: "window", includeUncontrolled: true }).then(cs => {
    const c = cs.find(x => x.url.startsWith(self.location.origin));
    return c ? c.focus() : self.clients.openWindow(url);
  }));
});
