// Performance check: how fast the page renders on a slow phone, to compare
// before and after a change. Not a test: it prints numbers and fails only if
// the page doesn't render.
//
//   node scripts/perf.js site [runs]     (site/stats.json from -demo -out, as for the smoke test)
//
// For each run it loads the page at 390 × 844 as a phone with the CPU slowed
// down 4× and measures:
//   render    from navigation to <html data-rendered="true">
//   1Y        switching the range from 30 days to a year: the click until
//             the next frame is painted
//   long      the main thread's long tasks (> 50 ms) during load, summed
//   heap      the JavaScript heap after loading
// and prints the median of each, plus the size of stats.json (raw and gzip).

import { readFile } from 'node:fs/promises';
import { gzipSync } from 'node:zlib';
import { join, resolve } from 'node:path';
import { findChrome, launch, serve } from './browser.js';

const root = resolve(process.argv[2] || 'site');
const runs = Number(process.argv[3] || 5);
const chrome = findChrome();
if (!chrome) {
  console.error('No Chrome found; set CHROME_BIN.');
  process.exit(2);
}

const raw = await readFile(join(root, 'stats.json'));
const stats = JSON.parse(raw);
const server = await serve(root);
const browser = await launch(chrome, { now: stats.generated_at ? Date.parse(stats.generated_at) : undefined });

// Recorded from the start of each page: when it marked itself rendered, and
// the long tasks until then.
await browser.send('Page.addScriptToEvaluateOnNewDocument', { source: `(() => {
  window.__perf = { long: 0 };
  try {
    new PerformanceObserver((l) => { for (const e of l.getEntries()) if (!window.__perf.rendered) window.__perf.long += e.duration; })
      .observe({ type: 'longtask', buffered: true });
  } catch {}
  // On the document: when this runs, <html> doesn't exist yet.
  new MutationObserver((_, o) => {
    if (document.documentElement?.dataset.rendered === 'true') { window.__perf.rendered = performance.now(); o.disconnect(); }
  }).observe(document, { attributes: true, subtree: true, attributeFilter: ['data-rendered'] });
})();` });
await browser.send('Emulation.setCPUThrottlingRate', { rate: 4 });
await browser.send('HeapProfiler.enable');

const results = [];
try {
  for (let i = 0; i < runs; i++) {
    await browser.view(server.base + 'index.html#range=30', { width: 390, height: 844, mobile: true });
    const r = await browser.eval(`new Promise((ok) => {
      const error = document.documentElement.dataset.error;
      if (!window.__perf.rendered || error) return ok({ error: error || 'did not render' });
      // Charts finish drawing a moment after the page marks itself rendered.
      setTimeout(() => {
        const btn = document.querySelector('#range [data-range="365"]');
        const t0 = performance.now();
        btn.click();
        requestAnimationFrame(() => requestAnimationFrame(() => ok({ render: window.__perf.rendered, long: window.__perf.long, range: performance.now() - t0 })));
      }, 1000);
    })`);
    if (r.error) {
      console.error(`FAIL run ${i + 1}: ${r.error}`);
      process.exitCode = 1;
      break;
    }
    await browser.send('HeapProfiler.collectGarbage');
    r.heap = (await browser.send('Runtime.getHeapUsage')).usedSize;
    results.push(r);
    console.log(`run ${i + 1}: render ${ms(r.render)}, 1Y ${ms(r.range)}, long ${ms(r.long)}, heap ${mb(r.heap)}`);
  }
} finally {
  await browser.close();
  server.close();
}

if (results.length) {
  const median = (k) => {
    const v = results.map((r) => r[k]).sort((a, b) => a - b);
    return v[Math.floor(v.length / 2)];
  };
  console.log(`\nmedian of ${results.length} at 390 px, CPU 4× slower:`);
  console.log(`  render to data-rendered  ${ms(median('render'))}`);
  console.log(`  range 30 days → 1 year   ${ms(median('range'))}`);
  console.log(`  long tasks during load   ${ms(median('long'))}`);
  console.log(`  JS heap after load       ${mb(median('heap'))}`);
  console.log(`  stats.json               ${kb(raw.length)} (${kb(gzipSync(raw).length)} gzip), ${stats.days?.length ?? 0} days`);
}

function ms(v) { return Math.round(v) + ' ms'; }
function mb(v) { return (v / 1048576).toFixed(1) + ' MB'; }
function kb(v) { return Math.round(v / 1024) + ' KB'; }
