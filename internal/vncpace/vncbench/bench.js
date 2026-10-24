// bench.js - open vncbench's page in headless Chromium and count the viewer's frame updates.
//   [INFLIGHT=3] node bench.js BASE WAIT SECS DELAY...
// Connects at once (the page is then what keeps the guest's display refreshing), waits WAIT
// seconds, then for each DELAY (ms each way, set on the proxy) settles 3 s and counts SECS
// seconds. One JSON line per delay.
const { chromium } = require(process.env.HOME + '/tools/playwright/node_modules/playwright');
const [base = 'http://127.0.0.1:7801', wait = '0', secs = '15', ...delays] = process.argv.slice(2);
(async () => {
  const browser = await chromium.launch({ args: ['--use-angle=swiftshader', '--enable-unsafe-swiftshader'] });
  const page = await browser.newPage({ viewport: { width: 1100, height: 820 } });
  await page.goto(base + '/bench.html?inflight=' + (process.env.INFLIGHT || 1));
  await page.waitForFunction(() => window.stats && window.stats.connected, null, { timeout: 15000 });
  await page.waitForTimeout(Number(wait) * 1000);
  for (const d of delays.length ? delays : ['0']) {
    await fetch(base + '/delay?ms=' + d, { method: 'POST' });
    await page.waitForTimeout(3000);
    const b0 = await (await fetch(base + '/stats')).json();
    await page.evaluate(() => window.restart());
    await page.waitForTimeout(Number(secs) * 1000);
    const st = await page.evaluate(() => ({ frames: stats.frames, gaps: stats.gaps, ms: performance.now() - stats.t0 }));
    const b1 = await (await fetch(base + '/stats')).json();
    const g = st.gaps.slice().sort((a, b) => a - b);
    const pct = p => g.length ? Math.round(g[Math.min(g.length - 1, Math.floor(p * g.length))]) : null;
    console.log(JSON.stringify({ delayMs: Number(d), fps: +(st.frames * 1000 / st.ms).toFixed(1),
      frames: st.frames, gapMs: { p50: pct(0.5), p90: pct(0.9), max: pct(1) },
      MBps: +((b1.fromVNC - b0.fromVNC) / st.ms / 1000).toFixed(2) }));
  }
  await browser.close();
})();
