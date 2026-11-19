// What the smoke test and the performance check share: a static server for a
// site folder, headless Chrome driven through the DevTools protocol, and a
// clock set to the day the data was made for.

import { createServer } from 'node:http';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { spawn, spawnSync } from 'node:child_process';
import { extname, join, normalize } from 'node:path';

const TYPES = {
  '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.json': 'application/json',
  '.webmanifest': 'application/manifest+json', '.png': 'image/png', '.jpg': 'image/jpeg', '.woff2': 'font/woff2', '.txt': 'text/plain', '.svg': 'image/svg+xml',
};

// serve serves the files under root as the site does without -serve: the
// page finds no API (api/me and api/stats answer 404), needs no sign-in and
// reads stats.json instead. It answers the base URL and close().
export async function serve(root) {
  const server = createServer(async (req, res) => {
    const path = normalize(decodeURIComponent(new URL(req.url, 'http://x').pathname)).replace(/^(\.\.[/\\])+/, '');
    try {
      const body = await readFile(join(root, path.endsWith('/') ? path + 'index.html' : path));
      res.writeHead(200, { 'content-type': TYPES[extname(path)] || 'application/octet-stream' });
      res.end(body);
    } catch {
      res.writeHead(404).end();
    }
  });
  await new Promise((ok) => server.listen(0, '127.0.0.1', ok));
  return { base: `http://127.0.0.1:${server.address().port}/`, close: () => server.close() };
}

// findChrome is the first Chrome that runs: CHROME_BIN, or one on the PATH.
export function findChrome() {
  return [process.env.CHROME_BIN, 'google-chrome', 'chromium', 'chromium-browser']
    .find((c) => c && spawnSync(c, ['--version']).status === 0);
}

// clockAt is a script that sets the page's clock to t (ms since 1970) as it
// loads and lets it run on from there, so "today" is the day the data was
// made for (go run ./cmd/eumetria -demo -now …).
export function clockAt(t) {
  return `(() => {
    const Real = Date, off = ${t} - Real.now();
    class Fixed extends Real {
      constructor(...a) { super(...(a.length ? a : [Real.now() + off])); }
      static now() { return Real.now() + off; }
    }
    globalThis.Date = Fixed;
  })();`;
}

// launch starts headless Chrome with one page. The page gets clockAt(now)
// when now is given. It answers the DevTools connection (connect) plus
// view(), which loads a URL at a size and waits until the page has rendered,
// and close(). A Chrome that doesn't come up is tried once more; the error
// then says its version, exit code and last words.
export async function launch(chrome, opts = {}) {
  try {
    return await launchOnce(chrome, opts);
  } catch (err) {
    console.error(`note: Chrome didn't start, trying once more: ${err.message}`);
    return launchOnce(chrome, opts);
  }
}

async function launchOnce(chrome, { now } = {}) {
  const profile = await mkdtemp(join(tmpdir(), 'eumetria-chrome-'));
  const p = spawn(chrome, ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--no-first-run',
    '--no-default-browser-check', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], {
    stdio: ['ignore', 'ignore', 'pipe'],
    // Chrome's own temporary files (.com.google.Chrome.*) into the profile,
    // which is removed afterwards: left in /tmp they pile up run after run.
    env: { ...process.env, TMPDIR: profile },
  });
  // The last of what Chrome says, for the error when it doesn't come up.
  let said = '';
  p.stderr.setEncoding('utf8');
  p.stderr.on('data', (s) => { said = (said + s).slice(-3000); });
  let gone = null;
  const exited = new Promise((ok) => p.once('exit', (code, signal) => { gone = { code, signal }; ok(); }));
  try {
    const cdp = await connect(profile, () => gone).catch((err) => {
      const version = spawnSync(chrome, ['--version'], { encoding: 'utf8' }).stdout?.split('\n')[0].trim() || chrome;
      const how = gone ? `it exited (${gone.signal || 'code ' + gone.code})` : 'it is still running';
      throw new Error(`${err.message}; ${version}, ${how}${said.trim() ? '. It said:\n' + said.trim() : ', and said nothing'}`);
    });
    await cdp.send('Page.enable');
    if (now) await cdp.send('Page.addScriptToEvaluateOnNewDocument', { source: clockAt(now) });
    return {
      ...cdp,
      // view loads url as a screen of width × height (mobile: a phone) and
      // waits up to 10 s for <html data-rendered="true"> or an error.
      async view(url, { width, height, mobile = false }) {
        await cdp.send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: mobile ? 2 : 1, mobile });
        // Through a blank page: a change of the hash alone doesn't reload.
        await cdp.load('about:blank');
        await cdp.load(url);
        await cdp.eval(`new Promise((ok) => {
          const t0 = performance.now();
          const check = () => {
            const d = document.documentElement.dataset;
            if (d.rendered === 'true' || d.error || performance.now() - t0 > 10000) return ok();
            setTimeout(check, 50);
          };
          check();
        })`);
      },
      async close() {
        cdp.close();
        p.kill();
        await exited;
        await removeProfile(profile);
      },
    };
  } catch (err) {
    p.kill();
    await exited;
    await removeProfile(profile);
    throw err;
  }
}

// removeProfile deletes Chrome's temporary profile. Chrome's helper processes
// can still write into it for a moment after the browser exits (ENOTEMPTY),
// so it tries again, and a leftover folder in the temp dir never fails a run.
async function removeProfile(profile) {
  try {
    await rm(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
  } catch (err) {
    console.error(`note: could not remove ${profile}: ${err.message}`);
  }
}

// connect opens the DevTools protocol on the page of the Chrome whose
// profile is profile: send(method, params) answers its result, eval(js)
// the value of an expression (a promise is awaited).
async function connect(profile, gone = () => null) {
  let port;
  // Up to 30 s: a fresh CI runner starts Chrome slowly the first time.
  for (let i = 0; i < 300 && !port && !gone(); i++) {
    port = (await readFile(join(profile, 'DevToolsActivePort'), 'utf8').catch(() => '')).split('\n')[0];
    if (!port) await new Promise((ok) => setTimeout(ok, 100));
  }
  if (!port) throw new Error('Chrome did not open its DevTools port');
  const targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
  const ws = new WebSocket(targets.find((t) => t.type === 'page').webSocketDebuggerUrl);
  await new Promise((ok, fail) => { ws.onopen = ok; ws.onerror = fail; });
  let id = 0;
  const pending = new Map();
  let loaded = null;
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.method === 'Page.loadEventFired') loaded?.();
    if (!pending.has(m.id)) return;
    const [ok, fail] = pending.get(m.id);
    pending.delete(m.id);
    m.error ? fail(new Error(m.error.message)) : ok(m.result);
  };
  const send = (method, params = {}) => new Promise((ok, fail) => {
    pending.set(++id, [ok, fail]);
    ws.send(JSON.stringify({ id, method, params }));
  });
  return {
    send,
    // load navigates to url and waits for its load event.
    load: async (url) => {
      const done = new Promise((ok) => { loaded = ok; });
      await send('Page.navigate', { url });
      await done;
    },
    eval: async (expression) => {
      const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
      return r.result.value;
    },
    close: () => ws.close(),
  };
}
