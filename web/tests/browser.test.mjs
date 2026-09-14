import test from 'node:test';
import assert from 'node:assert/strict';
import { access, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { constants as fsConstants } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { simulationFixture } from './simulation-fixture.mjs';

const browserSimulation = process.env.BROWSER_SIMULATION_FILE
  ? JSON.parse(await readFile(process.env.BROWSER_SIMULATION_FILE, 'utf8')) : simulationFixture;

const testsDirectory = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(testsDirectory, '..');
const repositoryRoot = resolve(webRoot, '..');
const panelSource = await readFile(process.env.BROWSER_PANEL_FILE || join(webRoot, 'panel.html'), 'utf8');
const harnessSource = await readFile(join(testsDirectory, 'browser-harness.js'), 'utf8');

const browserCandidates = [
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser',
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
];
const browserStartupTimeoutMs = 20_000;
const hostBrowser = await resolveBrowserExecutable(process.env.BROWSER_BIN || '', browserCandidates);
const dockerBrowserImage = process.env.BROWSER_DOCKER_IMAGE || '';
const browserRunner = hostBrowser || dockerBrowserImage ? 'available' : '';
const browserSkip = browserRunner ? undefined : 'headless Chromium is not installed; CI installs it for this suite';

test('browser process cleanup escalates and terminates the child process group', async () => {
  const script = `
    const { spawn } = require('node:child_process');
    process.on('SIGTERM', () => {});
    const descendant = spawn(process.execPath, ['-e', 'process.on("SIGTERM", () => {}); setInterval(() => {}, 1000);'], {
      stdio: ['ignore', 'ignore', 'ignore'],
    });
    descendant.once('spawn', () => process.send?.({ ready: true, descendantPid: descendant.pid }));
    setInterval(() => {}, 1000);
  `;
  const browser = spawn(process.execPath, ['-e', script], {
    detached: true,
    stdio: ['ignore', 'ignore', 'ignore', 'ipc'],
  });
  let closed = false;
  let descendantPid = 0;
  browser.once('close', () => {
    closed = true;
  });
  try {
    await new Promise((resolve, reject) => {
      browser.once('spawn', resolve);
      browser.once('error', reject);
    });
    await new Promise((resolve, reject) => {
      browser.once('message', (message) => {
        if (!message?.ready || !message.descendantPid) {
          reject(new Error(`unexpected child message: ${JSON.stringify(message)}`));
          return;
        }
        descendantPid = message.descendantPid;
        resolve();
      });
      browser.once('error', reject);
    });
    await terminateChild(browser, 25);
    assert.equal(closed, true);
    assert.equal(browser.signalCode, 'SIGKILL');
    assert.throws(() => process.kill(descendantPid, 0), { code: 'ESRCH' });
  } finally {
    try {
      process.kill(-browser.pid, 'SIGKILL');
    } catch (error) {
      if (error?.code !== 'ESRCH') throw error;
    }
  }
});

test('invalid explicit BROWSER_BIN fails instead of falling back or skipping', async () => {
  await assert.rejects(
    resolveBrowserExecutable('/definitely/not-a-browser', browserCandidates),
    /BROWSER_BIN is not executable: \/definitely\/not-a-browser/u,
  );
});

test('headless browser responsive viewports preserve essential state without page overflow', { skip: browserSkip }, async () => {
  const fixture = await startFixtureServer();
  try {
    for (const width of [375, 768, 1024, 1440]) {
      const result = await runBrowser(fixture.port, width, 900, 'responsive');
      assert.equal(result.ok, true, `${width}px: ${result.error || 'browser responsive check failed'}`);
      assert.equal(result.viewport.innerWidth, width, `${width}px viewport was not applied: ${JSON.stringify(result.viewport)}`);
      assert.ok(result.overflow.htmlScrollWidth <= result.viewport.innerWidth, `${width}px document overflow: ${JSON.stringify(result.overflow)}`);
      assert.ok(result.overflow.bodyScrollWidth <= result.viewport.innerWidth, `${width}px body overflow: ${JSON.stringify(result.overflow)}`);
      assert.deepEqual(result.essential, { summary: true, accounts: true, accountState: true, workspace: true, controls: true }, `${width}px essential state`);
    }
  } finally {
    await fixture.close();
  }
});

test('headless browser keeps scheduled membership separate and requires probe consent', { skip: browserSkip }, async () => {
  const fixture = await startFixtureServer();
  try {
    const result = await runBrowser(fixture.port, 1024, 900, 'contracts');
    assert.equal(result.ok, true, result.error || 'browser contract flow failed');
    assert.deepEqual(result.scheduleRequest.scheduled_account_keys, ['acct-browser']);
    assert.deepEqual(result.probeRequest.account_keys, ['acct-browser']);
    assert.equal(result.probeRequest.acknowledge_quota_effect, true);
    assert.equal(result.probeRequest.allow_unavailable, false);
  } finally {
    await fixture.close();
  }
});

test('simulator renders different A/B coverage with point markers and readable reference layout', { skip: browserSkip }, async () => {
  const fixture = await startFixtureServer();
  try {
    for (const [width, mode] of [[375, 'simulator'], [1440, 'simulator'], [1440, 'simulator-dark']]) {
      const result = await runBrowser(fixture.port, width, 1000, mode);
      assert.equal(result.ok, true, result.error);
      assert.notEqual(result.trackA, result.trackB, 'A/B tracks must not reuse the same segments');
      assert.equal(result.availableA, 2);
      assert.equal(result.availableB, 4);
      assert.equal(result.markers, 3, 'include the late-work renewal preheat');
      assert.deepEqual(result.preheatTimes, ['06:30', '11:30', '16:30']);
      assert.deepEqual(result.preheatWindows, ['06:00–07:00', '11:00–12:00', '16:00–17:00']);
      assert.equal(result.hasLegend, true);
      assert.equal(result.readable, true);
      assert.equal(result.pageOverflow, false);
      assert.equal(result.pageVersion, process.env.BROWSER_EXPECTED_VERSION || 'v0.0.0-test');
      assert.equal(result.theme, mode.endsWith('-dark') ? 'dark' : 'light');
      assert.match(result.assumptions, /单窗口预计可用 60 分钟/);
      if (process.env.BROWSER_PANEL_FILE) assert.deepEqual(result.assetRequests, [], 'embedded panel requested secondary resources');
      assert.deepEqual(result.emptySchedule.scheduled_account_keys, []);
    }
  } finally {
    await fixture.close();
  }
});

for (const [mode, width] of [
  ['audit-refresh', 1440], ['audit-refresh-empty', 1440], ['audit-refresh-failure', 1440],
  ['audit-refresh-progress', 1440], ['audit-background-refresh', 1440], ['audit-history-batches', 375],
  ['audit-draft', 1440], ['audit-validation', 1440], ['audit-axis', 1440],
  ['audit-accessibility', 375], ['audit-theme-dark', 1440], ['audit-navigation', 1440], ['audit-zero-gain', 1440],
]) {
  test(`ui audit regression: ${mode}`, { skip: browserSkip }, async () => {
    const fixture = await startFixtureServer();
    try {
      const result = await runBrowser(fixture.port, width, 1000, mode);
      assert.equal(result.ok, true, result.error);
    } finally { await fixture.close(); }
  });
}

async function findExecutable(explicit, candidates) {
  for (const candidate of [explicit, ...candidates]) {
    if (!candidate) continue;
    try {
      await access(candidate, fsConstants.X_OK);
      return candidate;
    } catch {
      // Continue through the known system locations.
    }
  }
  return '';
}

async function resolveBrowserExecutable(explicit, candidates) {
  const executable = await findExecutable(explicit, explicit ? [] : candidates);
  if (explicit && !executable) {
    throw new Error(`BROWSER_BIN is not executable: ${explicit}`);
  }
  return executable;
}

function jsonResponse(response, status, value) {
  const body = JSON.stringify(value);
  response.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(body),
  });
  response.end(body);
}

async function requestBody(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  const body = Buffer.concat(chunks).toString('utf8');
  return body ? JSON.parse(body) : null;
}

async function startFixtureServer() {
  const state = { quotaRefreshRequests: [], authFilesRequests: [], scheduleRequests: [], probeRequests: [], simulationRequests: [], assetRequests: [] };
  const server = createServer(async (request, response) => {
    try {
      const url = new URL(request.url || '/', 'http://127.0.0.1');
      if (url.pathname === '/__browser-state') {
        if (request.method === 'POST') state.controls = { ...state.controls, ...await requestBody(request) };
        jsonResponse(response, 200, state);
        return;
      }
      if (url.pathname === '/__browser-harness.js') {
        response.writeHead(200, { 'Content-Type': 'text/javascript; charset=utf-8' });
        response.end(harnessSource);
        return;
      }
      if (url.pathname === '/v0/resource/plugins/browser-test/panel') {
        response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
        const theme = url.searchParams.get('browser_test')?.endsWith('-dark') ? 'dark' : 'light';
        response.end(panelSource.replace('data-theme="light"', `data-theme="${theme}"`).replace('{{PLUGIN_VERSION}}', 'v0.0.0-test').replace('</body>', '<script src="/__browser-harness.js"></script></body>'));
        return;
      }
      if (url.pathname.startsWith('/v0/resource/plugins/browser-test/')) {
        const relative = url.pathname.slice('/v0/resource/plugins/browser-test/'.length);
        state.assetRequests.push(relative);
        const filePath = resolve(webRoot, relative);
        if (!filePath.startsWith(`${webRoot}/`) || filePath === webRoot) {
          response.writeHead(404);
          response.end();
          return;
        }
        try {
          const body = await readFile(filePath);
          const contentType = relative.endsWith('.css')
            ? 'text/css; charset=utf-8'
            : relative.endsWith('.html')
              ? 'text/html; charset=utf-8'
              : 'text/javascript; charset=utf-8';
          response.writeHead(200, { 'Content-Type': contentType });
          response.end(body);
        } catch {
          response.writeHead(404);
          response.end();
        }
        return;
      }
      if (url.pathname.startsWith('/v0/management/plugins/browser-test/')) {
        await serveManagementFixture(request, response, url.pathname, state);
        return;
      }
      if (url.pathname === '/v0/management/auth-files') {
        await serveManagementFixture(request, response, url.pathname, state);
        return;
      }
      response.writeHead(404);
      response.end();
    } catch (error) {
      jsonResponse(response, 500, { ok: false, error: String(error?.message || error) });
    }
  });
  await new Promise((resolvePromise, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolvePromise);
  });
  const address = server.address();
  return {
    port: typeof address === 'object' && address ? address.port : 0,
    state,
    close: () => new Promise((resolvePromise, reject) => server.close((error) => error ? reject(error) : resolvePromise())),
  };
}

async function serveManagementFixture(request, response, path, state) {
  if (request.method === 'GET' && path.endsWith('/status')) {
    jsonResponse(response, 200, { ok: true, result: { enabled: false, next_runs: {}, run_id: '', run_completed: 0, run_total: 0 } });
    return;
  }
      if (request.method === 'GET' && path.endsWith('/accounts')) {
    jsonResponse(response, 200, { ok: true, result: [{ account_key: 'acct-browser', email: 'browser@example.com', auth_index: '7', account_prefix: 'acct_browser', masked_identity: 'b***@example.com', plan_label: 'Pro', disabled: false, unavailable: false, fingerprint: 'browser-fp' }] });
    return;
  }
  if (request.method === 'GET' && path === '/v0/management/auth-files') {
    state.authFilesRequests.push({ at: Date.now() });
    if (state.controls?.authFailure) { jsonResponse(response, 503, { message: 'fixture discovery failed' }); return; }
    if (state.controls?.files) { jsonResponse(response, 200, { files: state.controls.files }); return; }
    jsonResponse(response, 200, { files: [{ provider: 'codex', email: 'browser@example.com', auth_index: 'browser', account_id: 'acct_browser', plan: 'Pro', disabled: false, unavailable: false }] });
    return;
  }
  if (request.method === 'GET' && path.endsWith('/schedule')) {
    jsonResponse(response, 200, {
      ok: true,
      result: {
        schema_version: 1,
        revision: 1,
        enabled: false,
        timezone: 'Asia/Shanghai',
        weekdays: [1, 2, 3, 4, 5],
        work_periods: [{ start: '09:00', end: '12:00' }, { start: '13:30', end: '19:00' }],
        preheat_lead_minutes: null,
        preheat_span_minutes: null,
        productivity_minutes: 60,
        window_hours: 5,
        health_threshold_percent: 80,
        skip_window_times: [],
        remaining_quota_floor_percent: 20,
        remaining_window_floor_minutes: 60,
        long_window_floor_percent: 10,
        blackout_periods: [],
        probe_model: 'gpt-5.6-luna',
        probe_timeout_seconds: 30,
        scheduled_account_keys: [],
      },
    });
    return;
  }
  if (request.method === 'PUT' && path.endsWith('/schedule')) {
    const body = await requestBody(request); state.scheduleRequests.push(body);
    if (state.controls?.saveDelay) await new Promise((resolve) => setTimeout(resolve, 200));
    jsonResponse(response, 200, { ok: true, result: { ...body, revision: 2 } });
    return;
  }
  if (request.method === 'GET' && path.endsWith('/quota-snapshot')) {
    if (state.controls?.quotaDelay) await new Promise((resolve) => setTimeout(resolve, state.controls.quotaDelay));
    jsonResponse(response, 200, { ok: true, result: [] });
    return;
  }
  if (request.method === 'GET' && path.endsWith('/history')) {
    if (state.controls?.historyDelay) await new Promise((resolve) => setTimeout(resolve, state.controls.historyDelay));
    jsonResponse(response, 200, { ok: true, result: state.controls?.history || [] });
    return;
  }
  if (request.method === 'POST' && path.endsWith('/simulate')) {
    state.simulationRequests.push(await requestBody(request));
    jsonResponse(response, 200, { ok: true, result: state.controls?.zeroGain ? { ...browserSimulation, scheduled: browserSimulation.baseline, net_gain_minutes: 0 } : browserSimulation });
    return;
  }
  if (request.method === 'POST' && path.endsWith('/probes')) {
    const body = await requestBody(request); state.probeRequests.push(body);
    jsonResponse(response, 202, { ok: true, result: { run_id: 'run-browser' } });
    return;
  }
      if (request.method === 'POST' && path.endsWith('/quota-refresh')) {
    const body = await requestBody(request);
    state.quotaRefreshRequests.push(body);
    if (state.controls?.refreshDelay) await new Promise((resolve) => setTimeout(resolve, state.controls.refreshDelay));
    const keys = Array.isArray(body?.account_keys) ? body.account_keys : [];
    const failing = new Set((state.controls?.failAuthIndexes || []).map((authIndex) => `acct-${authIndex}`));
    const result = keys.map((key) => (failing.has(key) ? failedQuotaView(key) : quotaView(key)));
    jsonResponse(response, 200, { ok: true, result });
        return;
      }
  jsonResponse(response, 404, { ok: false, error: { code: 'not_found', message: 'not found' } });
}

function quotaView(accountKey = 'acct-browser') {
  return {
    stale: false,
    refresh_error_code: '',
    snapshot: {
      account_key: accountKey,
      captured_at: new Date().toISOString(),
      reset_info_complete: true,
      reset_applicable_count: 2,
      windows: [{ short: true, duration_minutes: 60, remaining_percent: 80 }],
    },
  };
}

function failedQuotaView(accountKey) {
  return {
    stale: true,
    refresh_error_code: 'quota_refresh_failed',
    snapshot: { account_key: accountKey, captured_at: new Date().toISOString(), reset_info_complete: false, windows: [] },
  };
}

async function runBrowser(port, width, height, mode) {
  const profile = await mkdtemp(join(tmpdir(), 'codex-window-reset-browser-'));
  const page = `http://127.0.0.1:${port}/v0/resource/plugins/browser-test/panel?browser_test=${mode}`;
  const debugPort = await findFreePort();
  const chromeArgs = [
    '--headless=new',
    '--no-sandbox',
    '--disable-gpu',
    '--disable-dev-shm-usage',
    '--no-first-run',
    '--no-default-browser-check',
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${dockerBrowserImage ? '/tmp/codex-window-reset-browser' : profile}`,
    'about:blank',
  ];
  let browser;
  try {
    const invocation = dockerBrowserImage
      ? { file: 'docker', args: ['run', '--rm', '--network', 'host', '--entrypoint', '/usr/bin/google-chrome', dockerBrowserImage, ...chromeArgs] }
      : { file: hostBrowser, args: chromeArgs };
    browser = spawn(invocation.file, invocation.args, { cwd: repositoryRoot, detached: true, stdio: ['ignore', 'ignore', 'ignore'] });
    const { webSocketDebuggerUrl } = await waitForDevTools(debugPort, browser);
    const cdp = await connectDevTools(webSocketDebuggerUrl);
    await cdp.send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
    // Headless pages can retain activeElement while suppressing CSS :focus.
    await cdp.send('Emulation.setFocusEmulationEnabled', { enabled: true });
    if (mode.endsWith('-dark')) await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'dark' }] });
    if (mode === 'audit-navigation') await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
    await cdp.send('Page.enable');
    await cdp.send('Runtime.enable');
    await cdp.send('Page.navigate', { url: page });
    const result = await waitForBrowserResult(cdp);
    if (process.env.SCREENSHOT_DIR) {
      await mkdir(process.env.SCREENSHOT_DIR, { recursive: true });
      const screenshot = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
      await writeFile(join(process.env.SCREENSHOT_DIR, `${mode}-${width}.png`), Buffer.from(screenshot.data, 'base64'));
    }
    await cdp.close();
    return result;
  } catch (error) {
    throw new Error(`headless browser failed: ${error?.stack || error}`);
  } finally {
    await terminateChild(browser);
    await rm(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  }
}

async function terminateChild(child, graceMs = 2000) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;

  let timer;
  let closed = false;
  const close = new Promise((resolvePromise) => {
    child.once('close', () => {
      closed = true;
      resolvePromise();
    });
  });

  signalProcessGroup(child.pid, 'SIGTERM');
  const timeout = new Promise((resolvePromise) => {
    timer = setTimeout(resolvePromise, graceMs);
    timer.unref?.();
  });
  await Promise.race([close, timeout]);
  clearTimeout(timer);
  if (processGroupExists(child.pid)) signalProcessGroup(child.pid, 'SIGKILL');
  if (!closed) await close;
  await waitForProcessGroupExit(child.pid, graceMs);
}

function signalProcessGroup(pid, signal) {
  try {
    process.kill(-pid, signal);
  } catch (error) {
    if (error?.code !== 'ESRCH') throw error;
  }
}

function processGroupExists(pid) {
  try {
    process.kill(-pid, 0);
    return true;
  } catch (error) {
    if (error?.code === 'ESRCH') return false;
    throw error;
  }
}

async function waitForProcessGroupExit(pid, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (processGroupExists(pid)) {
    if (Date.now() >= deadline) throw new Error(`browser process group ${pid} did not exit`);
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 20));
  }
}

async function findFreePort() {
  const server = await import('node:net').then(({ createServer: createNetServer }) => new Promise((resolvePromise, reject) => {
    const listener = createNetServer();
    listener.once('error', reject);
    listener.listen(0, '127.0.0.1', () => {
      const address = listener.address();
      listener.close((error) => error ? reject(error) : resolvePromise(typeof address === 'object' && address ? address.port : 0));
    });
  }));
  return server;
}

async function waitForDevTools(port, browser) {
  const deadline = Date.now() + browserStartupTimeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    if (browser.exitCode !== null) throw new Error(`browser exited with ${browser.exitCode}`);
    try {
      const response = await fetch(`http://127.0.0.1:${port}/json/list`);
      const pages = await response.json();
      const page = pages.find((target) => target.type === 'page' && target.webSocketDebuggerUrl);
      if (page) return page;
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 50));
  }
  throw new Error(`DevTools endpoint did not start: ${lastError || 'timeout'}`);
}

async function connectDevTools(url) {
  const socket = new WebSocket(url);
  const pending = new Map();
  let nextID = 1;
  const opened = new Promise((resolvePromise, reject) => {
    socket.addEventListener('open', resolvePromise, { once: true });
    socket.addEventListener('error', reject, { once: true });
  });
  socket.addEventListener('message', (event) => {
    const message = JSON.parse(event.data);
    if (!message.id) return;
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    if (message.error) request.reject(new Error(message.error.message));
    else request.resolve(message.result);
  });
  await opened;
  return {
    send(method, params = {}) {
      const id = nextID++;
      return new Promise((resolvePromise, reject) => {
        pending.set(id, { resolve: resolvePromise, reject });
        socket.send(JSON.stringify({ id, method, params }));
      });
    },
    async evaluate(expression) {
      const result = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      return result?.result?.value;
    },
    close() {
      socket.close();
    },
  };
}

async function waitForBrowserResult(cdp) {
  const deadline = Date.now() + 8000;
  while (Date.now() < deadline) {
    const result = await cdp.evaluate('document.getElementById("browser-test-result")?.textContent || ""');
    if (result) return JSON.parse(result);
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 50));
  }
  throw new Error('browser harness result missing');
}
