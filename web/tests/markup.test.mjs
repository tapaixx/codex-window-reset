import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { promisify } from 'node:util';
import { syncHostTheme } from '../modules/main.js';

const root = resolve(new URL('..', import.meta.url).pathname);
const repositoryRoot = resolve(new URL('../..', import.meta.url).pathname);
const execFileAsync = promisify(execFile);
const productionFiles = [
  'panel.html',
  'styles.css',
  'modules/api.js',
  'modules/state.js',
  'modules/accounts.js',
  'modules/schedule.js',
  'modules/simulator.js',
  'modules/history.js',
  'modules/main.js',
];

async function readProduction(name) {
  return readFile(resolve(root, name), 'utf8');
}

test('panel IDs are unique and tab panels are linked', async () => {
  const html = await readProduction('panel.html');
  const ids = [...html.matchAll(/\sid=["']([^"']+)["']/g)].map((match) => match[1]);
  assert.equal(new Set(ids).size, ids.length);
  for (const tab of html.matchAll(/role=["']tab["'][^>]*id=["']([^"']+)["'][^>]*aria-controls=["']([^"']+)["']/g)) {
    assert.match(html, new RegExp(`id=["']${tab[2]}["']`));
  }
  assert.match(html, /<dialog\b[^>]*id=["']reset-dialog["']/);
  assert.match(html, /data-identity-state=["']masked["']/);
});

test('static buttons have accessible names and inputs have labels', async () => {
  const html = await readProduction('panel.html');
  for (const match of html.matchAll(/<button\b([^>]*)>([\s\S]*?)<\/button>/gi)) {
    const attributes = match[1];
    const contents = match[2].replace(/<[^>]+>/g, '').trim();
    assert.ok(contents || /aria-label=["'][^"']+["']/.test(attributes), `unnamed button: ${match[0]}`);
  }
  for (const match of html.matchAll(/<input\b([^>]*)>/gi)) {
    const attributes = match[1];
    if (/type=["']hidden["']/.test(attributes)) continue;
    assert.match(attributes, /(?:id|name)=["'][^"']+["']/);
  }
  assert.match(html, /<label\b[^>]*for=/i);
});

test('all production asset imports resolve and forbidden credential fallbacks are absent', async () => {
  const sources = await Promise.all(productionFiles.map(readProduction));
  const all = sources.join('\n');
  assert.equal(/access_token|management key|localStorage|sessionStorage/i.test(all), false);
  for (const source of sources) {
    for (const match of source.matchAll(/\bfrom\s+["'](\.\/[^"']+\.js)["']/g)) {
      assert.ok(await readFile(resolve(root, 'modules', match[1].replace(/^\.\//, '')), 'utf8'));
    }
  }
  const html = await readProduction('panel.html');
  const script = html.match(/<script[^>]+src=["']([^"']+\.js)["']/)?.[1];
  assert.ok(script);
  assert.ok(await readFile(resolve(root, script.replace(/^\.\//, '')), 'utf8'));
});

test('module exports and design tokens stay unique and exact', async () => {
  const sources = await Promise.all(productionFiles.slice(2).map(readProduction));
  const names = [];
  for (const source of sources) {
    for (const match of source.matchAll(/export\s+(?:async\s+)?(?:function|const|let|class)\s+([A-Za-z_$][\w$]*)/g)) names.push(match[1]);
  }
  assert.equal(new Set(names).size, names.length);
  const css = await readProduction('styles.css');
  for (const [name, value] of Object.entries({
    primary: '#2563eb', background: '#f8fafc', surface: '#fff', foreground: '#1e293b',
    muted: '#475569', border: '#e2e8f0', success: '#059669', warning: '#d97706',
    destructive: '#dc2626', focus: '#2563eb',
  })) assert.match(css, new RegExp(`--${name}\\s*:\\s*${value.replace('#', '\\#')}`));
  assert.match(css, /max-width\s*:\s*767px/);
  assert.match(css, /prefers-reduced-motion\s*:\s*reduce/);
  assert.match(css, /:focus-visible\s*\{/);
  assert.match(css, /button, input, select, textarea\s*\{[^{}]*min-height:\s*44px/);
});

function contrastRatio(foreground, background) {
  const channel = (hex, offset) => {
    const value = Number.parseInt(hex.slice(offset, offset + 2), 16) / 255;
    return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  };
  const luminance = (hex) => 0.2126 * channel(hex, 1) + 0.7152 * channel(hex, 3) + 0.0722 * channel(hex, 5);
  const light = luminance(foreground) + 0.05;
  const dark = luminance(background) + 0.05;
  return Math.max(light, dark) / Math.min(light, dark);
}

test('normal-text token pairs meet WCAG contrast', () => {
  for (const [foreground, background] of [
    ['#1e293b', '#ffffff'], ['#475569', '#ffffff'], ['#2563eb', '#ffffff'], ['#dc2626', '#ffffff'],
  ]) assert.ok(contrastRatio(foreground, background) >= 4.5, `${foreground} on ${background}`);
});

test('theme synchronization reads a parent dark signal without polling', () => {
  const makeRoot = (theme = null, dark = false) => ({
    dataset: theme ? { theme } : {},
    getAttribute(name) { return name === 'data-theme' ? theme : null; },
    setAttribute(name, value) { if (name === 'data-theme') this.dataset.theme = value; },
    classList: { contains(name) { return dark && name === 'dark'; } },
  });
  const panelRoot = makeRoot();
  const parentRoot = makeRoot('dark');
  syncHostTheme({ root: panelRoot, parentRoot, observe: false });
  assert.equal(panelRoot.dataset.theme, 'dark');
});

test('production web modules use no unapproved dynamic imports', async () => {
  const sources = await Promise.all(productionFiles.map(readProduction));
  assert.equal(sources.some((source) => /\bimport\s*\(/u.test(source)), false);
});

test('the disposable Task 11 report is no longer tracked', async () => {
  const report = '.superpowers/sdd/2026-09-09-codex-window-reset/task-11-report.md';
  const { stdout } = await execFileAsync('git', ['ls-files', '--', report], { cwd: repositoryRoot });
  assert.equal(stdout.trim(), '');
});
