(async () => {
  const output = document.createElement('pre');
  output.id = 'browser-test-result';
  output.hidden = true;
  document.body.append(output);

  const result = { ok: false, clientErrors: [] };
  window.addEventListener('error', (event) => result.clientErrors.push(`error: ${event.error?.stack || event.message || 'unknown'}`));
  window.addEventListener('unhandledrejection', (event) => result.clientErrors.push(`rejection: ${event.reason?.stack || event.reason || 'unknown'}`));
  try {
    const mode = new URL(location.href).searchParams.get('browser_test');
    if (mode?.startsWith('audit-')) await checkUIAudit(mode);
    else if (mode?.startsWith('simulator')) await checkSimulator(result);
    else if (mode === 'contracts') await checkScheduleAndProbeContracts(result);
    else await checkResponsiveLayout(result);
    result.ok = true;
  } catch (error) {
    result.error = `${error?.stack || String(error)}${result.clientErrors.length ? `\nClient errors:\n${result.clientErrors.join('\n')}` : ''}`;
  }
  output.textContent = JSON.stringify(result);
})();

async function checkUIAudit(mode) {
  await waitForPanel();
  const $ = (selector) => document.querySelector(selector);
  const assert = (value, message) => { if (!value) throw new Error(message); };
  const configure = (controls) => fetch('/__browser-state', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(controls) });
  const set = (name, value) => { const input = $(`[name="${name}"]`); input.value = value; input.dispatchEvent(new Event('input', { bubbles: true })); };
  const save = () => $('#schedule-form button[type=submit]').click();
  const refresh = async () => {
    $('[data-action="refresh-all-quota"]').click();
    await waitFor(() => !$('[data-action="refresh-all-quota"]').disabled, 'refresh-all did not complete');
  };
  const file = (auth_index, disabled = false) => ({ provider: 'codex', auth_index, email: `${auth_index}@example.com`, account_id: `acct_${auth_index}`, disabled });
  if (mode === 'audit-refresh') {
    $('[data-account-selection]').click();
    $('[data-account-scheduled]').click();
    // A disabled credential becomes enabled in the host; a new one is discovered too.
    await configure({ files: [file('browser'), file('revived', true)] }); await refresh();
    await configure({ files: [file('browser'), file('revived'), file('new'), file('disabled', true)], failAuthIndexes: ['browser'] });
    const before = (await fixtureState()).quotaRefreshRequests.length;
    await refresh();
    const requests = (await fixtureState()).quotaRefreshRequests.slice(before);
    // Refresh-all sends no keys: the plugin already discovers accounts for the
    // batch, so the panel must not fetch the credential list and echo it back.
    assert(requests.length === 1 && !requests[0].account_keys?.length, `refresh-all sent an echoed key list: ${JSON.stringify(requests)}`);
    // Newly discovered and re-enabled credentials still get refreshed and shown.
    for (const key of ['acct-revived', 'acct-new', 'acct-disabled']) {
      assert($(`#accounts-table [data-account-key="${key}"]`), `${key} did not appear after refresh-all`);
    }
    assert($('#accounts-table [data-account-key="acct-revived"]').textContent.includes('80%'), 'successful quota lost when another account fails');
    assert($('#accounts-table [data-account-key="acct-browser"]').textContent.includes('已过期'), 'failed quota was not marked stale');
    assert($('[data-account-selection="acct-browser"]').checked, 'manual selection was lost');
    assert($('[data-account-scheduled="acct-browser"]').checked && !$('[data-account-scheduled="acct-revived"]').checked, 'refresh changed scheduled membership');
    assert(/失败\s*1/.test($('#account-feedback').textContent), 'partial failure summary missing');
    assert(!$('#feedback').textContent.includes('刷新全部额度'), 'partial failure falsely announced total success');
  } else if (mode === 'audit-refresh-progress') {
    // Quota refresh is now one plugin round trip that returns usage and
    // reset-credit data together, so there is no separate pending phase to
    // observe; the invariant worth guarding is that the UI stays inert while
    // the single request is outstanding and updates atomically once it lands.
    await configure({ refreshDelay: 1500 });
    $('[data-action="refresh-all-quota"]').click();
    assert($('[data-action="refresh-all-quota"]').disabled, 'refresh button did not disable while the request is outstanding');
    assert(!$('#accounts-table').textContent.includes('80%'), 'usage appeared before the plugin response arrived');
    await waitFor(() => $('#accounts-table').textContent.includes('80%'), 'usage did not appear after refresh completed');
    assert(!$('[data-action="refresh-all-quota"]').disabled, 'refresh did not finish');
    assert(!$('#accounts-table').textContent.includes('获取重置次数中'), 'obsolete two-phase pending state leaked into the single-shot refresh');
  } else if (mode === 'audit-background-refresh') {
    await configure({ quotaDelay: 1600, historyDelay: 1600 });
    $('[data-action="refresh-panel"]').click();
    await waitFor(() => $('#connection').dataset.state === 'loading', 'panel refresh did not begin');
    $('[data-action="refresh-all-quota"]').click();
    await waitFor(() => $('#accounts-table').textContent.includes('80%'), 'background panel load blocked explicit quota refresh');
    assert($('#connection').dataset.state === 'loading', 'fixture did not exercise overlapping requests');
    await waitFor(() => $('#connection').dataset.state === 'ready', 'panel did not finish');
    assert($('#accounts-table').textContent.includes('80%'), 'late cached quota overwrote fresh usage');
  } else if (mode === 'audit-history-batches') {
    await configure({ history: [
      { id: 'a', trigger: 'preheat', occurrence_id: '2026-09-14/p0/a', account_key: 'a', masked_identity: 'a***', started_at: '2026-09-14T08:15:00+08:00' },
      { id: 'b', trigger: 'preheat', occurrence_id: '2026-09-14/p0/b', account_key: 'b', masked_identity: 'b***', started_at: '2026-09-14T08:20:00+08:00' },
      { id: 'c', trigger: 'compensation', occurrence_id: '2026-09-14/p0/a', account_key: 'a', masked_identity: 'a***', started_at: '2026-09-14T08:25:00+08:00' },
      { id: 'd', trigger: 'preheat', occurrence_id: '2026-09-14/p1/a', account_key: 'a', started_at: '2026-09-14T13:20:00+08:00' },
    ] });
    $('[data-action="load-records"]').click();
    await waitFor(() => document.querySelectorAll('#history-output details').length === 2, 'staggered preheats were not grouped by planned period');
    const batch = $('#history-output [data-batch-key="scheduled/2026-09-14/p0"]');
    assert(batch.querySelector('summary').textContent.includes('2 个账号 · 3 次操作'), 'compensation inflated unique account count');
    batch.querySelector('summary').click();
    assert(batch.open && batch.querySelectorAll('.history-batch-item').length === 3, 'batch cannot expand to account records');
    $('[data-action="load-records"]').click();
    await waitFor(() => $('#history-output [data-batch-key="scheduled/2026-09-14/p0"]') !== batch, 'history did not refresh');
    assert($('#history-output [data-batch-key="scheduled/2026-09-14/p0"]').open, 'history refresh collapsed the inspected batch');
    assert(document.documentElement.scrollWidth <= innerWidth, 'grouped history overflows mobile viewport');
  } else if (mode === 'audit-refresh-empty') {
    await configure({ files: [file('disabled', true)] });
    const before = (await fixtureState()).quotaRefreshRequests.length; await refresh();
    const requests = (await fixtureState()).quotaRefreshRequests.slice(before);
    assert(requests.length === 1, 'refresh-all did not reach the plugin');
    // A host that exposes only disabled credentials must still be refreshed:
    // quota refresh is a read-only diagnostic, not a probe.
    assert($('#accounts-table [data-account-key="acct-disabled"]')?.textContent.includes('80%'), 'disabled account was not refreshed');
    assert(!/没有可刷新的已启用账号/.test($('#account-feedback').textContent), 'disabled account was incorrectly reported as skipped');
  } else if (mode === 'audit-refresh-failure') {
    await configure({ failAuthIndexes: ['browser'] }); await refresh();
    assert(/失败\s*1/.test($('#account-feedback').textContent), 'all-failure summary missing');
    assert(!/全部.*刷新|刷新全部额度/.test($('#feedback').textContent), 'failed request announced success');
    await configure({ authFailure: true }); await refresh();
    assert($('[data-account-selection]'), 'failed discovery destroyed existing accounts');
  } else if (mode === 'audit-draft') {
    set('window_hours', '6'); $('[data-account-scheduled]').click();
    const event = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(event);
    assert(event.defaultPrevented, 'unsaved navigation is not guarded');
    $('[data-action="refresh-panel"]').click();
    await waitFor(() => $('#connection').dataset.state === 'ready', 'panel refresh failed');
    assert($('[name="window_hours"]').value === '6' && $('[data-account-scheduled]').checked, 'header refresh overwrote the draft');
    assert($('#schedule-dirty')?.textContent.includes('未保存'), 'dirty indicator missing');
    await configure({ saveDelay: true }); save();
    assert($('#schedule-form button[type=submit]').textContent.includes('保存中'), 'save loading state missing');
    set('window_hours', '7');
    await waitFor(() => !$('#schedule-form button[type=submit]').disabled, 'save did not complete');
    assert($('[name="window_hours"]').value === '7', 'save response overwrote edits made during save');
    save(); await waitFor(() => !$('#schedule-form button[type=submit]').disabled, 'second save did not complete');
    const clean = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(clean);
    assert(!clean.defaultPrevented, 'saved form still warns about unsaved changes');
  } else if (mode === 'audit-validation') {
    const before = (await fixtureState()).scheduleRequests.length;
    set('timezone', 'Invalid/Zone'); save();
    assert($('[name="timezone"]').getAttribute('aria-invalid') === 'true', 'invalid timezone lacks field error');
    assert(document.activeElement === $('[name="timezone"]'), 'first invalid field did not receive focus');
    assert(document.getElementById($('[name="timezone"]').getAttribute('aria-describedby'))?.textContent, 'field error is not associated with input');
    assert((await fixtureState()).scheduleRequests.length === before, 'invalid draft was submitted');
    set('timezone', 'Asia/Shanghai'); set('work_start', '15:00'); save();
    assert($('[name="lunch_start"]').getAttribute('aria-invalid') === 'true', 'invalid work/lunch ordering not identified');
    set('work_start', '09:00'); set('preheat_lead_minutes', '120'); set('preheat_span_minutes', ''); save();
    assert($('[name="preheat_span_minutes"]').getAttribute('aria-invalid') === 'true', 'paired preheat requirement missing');
    set('preheat_span_minutes', '60'); set('probe_timeout_seconds', '1'); save();
    assert($('#schedule-form details').open && document.activeElement === $('[name="probe_timeout_seconds"]'), 'advanced invalid field stays hidden');
    set('probe_timeout_seconds', '30'); set('health_threshold_percent', '0'); set('remaining_quota_floor_percent', '0'); set('long_window_floor_percent', '0'); save();
    const state = await waitFor(async () => { const state = await fixtureState(); return state.scheduleRequests.length > before && state; }, 'valid config not saved');
    assert(state.scheduleRequests.at(-1).health_threshold_percent === 0 && state.scheduleRequests.at(-1).remaining_quota_floor_percent === 0 && state.scheduleRequests.at(-1).long_window_floor_percent === 0, 'valid zero thresholds replaced by defaults');
  } else if (mode === 'audit-axis') {
    await waitFor(() => $('.timeline-lane'), 'axis not rendered');
    const ticks = [...document.querySelectorAll('.timeline-ruler span')];
    assert(ticks[0].textContent === '05:00' && ticks.at(-1).textContent === '20:00', 'axis still wastes space outside work/preheat context');
    const start = $('.timeline-lane [data-kind="available"]').getBoundingClientRect().left;
    const marker = $('.work-boundary').getBoundingClientRect().left;
    assert(Math.abs(start - marker) < 2, `work boundary misaligned by ${start - marker}px`);
    assert($('.sim-metric.gain strong')?.textContent === '+1小时', 'preheat renewal benefit is missing from metrics');
    assert($('.strategy-a').textContent.includes('上班后第一次使用才开启窗口') && $('.strategy-b').textContent.includes('上班前先使用自动预热'), 'A/B explanations are unclear');
    set('work_start', '02:15');
    await waitFor(() => $('.timeline-ruler span')?.textContent === '01:00', 'axis did not follow edited work time');
    assert([...document.querySelectorAll('.timeline-ruler span')].at(-1).textContent === '20:00', 'odd-hour domain lost its final tick');
  } else if (mode === 'audit-zero-gain') {
    await configure({ zeroGain: true }); set('window_hours', '24');
    await waitFor(() => $('.sim-metrics')?.textContent.includes('当前参数下无额外收益'), 'zero gain not explained');
    assert(!$('.sim-metric.gain'), 'zero gain presented as positive');
  } else if (mode === 'audit-accessibility') {
    assert($('[data-account-selection]').getBoundingClientRect().width > 0, 'mobile individual account checkbox is hidden');
    const skip = $('#skip-window-input');
    assert(skip.labels.length || skip.getAttribute('aria-label'), 'skip time input has no accessible label');
    assert($('[data-action="refresh-quota"]').textContent.includes('刷新已选额度'), 'selected refresh scope unclear');
    assert($('[data-action="refresh-quota"]').getBoundingClientRect().width >= 130, 'mobile refresh label is squeezed into several lines');
    for (const dialog of document.querySelectorAll('dialog')) assert(document.getElementById(dialog.getAttribute('aria-labelledby'))?.textContent, `dialog ${dialog.id} has no name`);
    const input = $('#schedule-enabled'); input.focus();
    assert(getComputedStyle(input.nextElementSibling).outlineStyle !== 'none', 'switch has no visible focus');
  } else if (mode === 'audit-theme-dark') {
    assert(getComputedStyle(document.documentElement).colorScheme === 'dark', 'page color-scheme stayed light');
    for (const selector of ['.topbar', '.accounts-panel', '.history-panel', '.toolbar']) {
      const color = getComputedStyle($(selector)).backgroundColor.match(/\d+/g)?.slice(0, 3).map(Number);
      assert(color?.every((v) => v < 100), `${selector} stayed light in dark mode`);
    }
    await waitFor(() => $('.work-boundary'), 'dark timeline did not render');
    for (const selector of ['.work-boundary', '.weekday-chip:has(input:checked)']) {
      const style = getComputedStyle($(selector));
      const luminance = (color) => { const channels = color.match(/\d+/g).slice(0, 3).map((v) => { const c = Number(v) / 255; return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; }); return channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722; };
      const foreground = luminance(style.color), background = luminance(getComputedStyle($('.simulation-workspace')).backgroundColor);
      assert((Math.max(foreground, background) + .05) / (Math.min(foreground, background) + .05) >= 4.5, `${selector} text has insufficient dark contrast`);
    }
  } else if (mode === 'audit-navigation') {
    const scroll = Element.prototype.scrollIntoView; let options;
    Element.prototype.scrollIntoView = function (value) { options = value; scroll.call(this, value); };
    $('[data-action="focus-settings"]').click();
    assert(options?.behavior !== 'smooth', 'reduced motion ignored for settings navigation');
    assert($('#settings').getBoundingClientRect().top >= $('.topbar').getBoundingClientRect().bottom, 'sticky header obscures settings');
  }
}

async function checkSimulator(result) {
  await waitForPanel();
  await waitFor(() => document.querySelector('.timeline-lane'), 'simulator did not render');
  const [a, b] = document.querySelectorAll('.timeline-lane');
  result.trackA = a.innerHTML;
  result.trackB = b.innerHTML;
  if (result.trackA === result.trackB) throw new Error('A/B time axes incorrectly display identical segments');
  result.availableA = a.querySelectorAll('[data-kind="available"]').length;
  result.availableB = b.querySelectorAll('[data-kind="available"]').length;
  const ranges = (lane) => [...lane.querySelectorAll('[data-kind="available"]')].map((band) => band.title.replace('预计可用 ', ''));
  if (JSON.stringify(ranges(a)) !== JSON.stringify(['09:00–10:00', '14:00–15:00'])) throw new Error(`A incorrectly renews at lunch: ${ranges(a)}`);
  if (JSON.stringify(ranges(b)) !== JSON.stringify(['09:00–10:00', '11:30–12:00', '13:30–14:00', '16:30–17:30'])) throw new Error(`B is still limited after preheat-window renewal: ${ranges(b)}`);
  if (document.querySelector('.sim-metric.gain strong')?.textContent !== '+1小时') throw new Error('metric does not match the 60-minute timeline gain');
  const markers = [...b.querySelectorAll('[data-preheat-marker]')];
  result.markers = markers.length;
  result.preheatTimes = markers.map((node) => node.querySelector('.preheat-label')?.textContent);
  result.preheatWindows = [...document.querySelectorAll('.window-overview .window-pill strong')].map((node) => node.textContent);
  if (markers.some((node) => node.getBoundingClientRect().width > 30)) throw new Error('point marker expanded into a duration band');
  result.hasLegend = ['预计可用', '预计受限', '午休', '预热'].every((label) => document.querySelector('.timeline-legend')?.textContent.includes(label));
  result.readable = parseFloat(getComputedStyle(document.querySelector('.timeline-ruler span')).fontSize) >= 12;
  result.pageOverflow = document.documentElement.scrollWidth > innerWidth;
  result.pageVersion = document.querySelector('.version').textContent;
  result.theme = document.documentElement.dataset.theme;
  result.assumptions = document.querySelector('.simulation-assumptions').textContent;
  if (!result.assumptions.includes('单账户示例') || !result.assumptions.includes('午休不消耗')) throw new Error('simulation does not disclose its account and budget assumptions');
  result.assetRequests = (await fixtureState()).assetRequests;
  const beforeRefreshAll = await fixtureState();
  document.querySelector('[data-action="refresh-all-quota"]').click();
  // Refresh-all is one plugin round trip: the plugin discovers accounts for the
  // batch, so the panel no longer fetches the credential list first.
  const afterRefreshAll = await waitFor(async () => {
    const state = await fixtureState();
    return state.quotaRefreshRequests.length > beforeRefreshAll.quotaRefreshRequests.length ? state : null;
  }, 'refresh-all quota did not reach the plugin');
  result.refreshAll = { authFiles: afterRefreshAll.authFilesRequests.length - beforeRefreshAll.authFilesRequests.length, quotaCalls: afterRefreshAll.quotaRefreshRequests.length - beforeRefreshAll.quotaRefreshRequests.length };
  const focusPoint = document.querySelector('[data-preheat-marker]');
  focusPoint?.focus();
  if (focusPoint && getComputedStyle(focusPoint.querySelector('.preheat-tooltip')).display === 'none') throw new Error(`preheat detail is unavailable to keyboard focus: ${JSON.stringify({ active: document.activeElement?.outerHTML, connected: focusPoint.isConnected, focused: focusPoint.matches(':focus'), disabled: focusPoint.disabled })}`);
  focusPoint?.blur();
  const before = (await fixtureState()).scheduleRequests.length;
  document.querySelector('#schedule-enabled').checked = true;
  document.querySelector('[name="preheat_lead_minutes"]').value = '120';
  document.querySelector('[name="preheat_span_minutes"]').value = '60';
  document.querySelector('#schedule-form button[type="submit"]').click();
  const saved = await waitFor(async () => {
    const state = await fixtureState();
    return state.scheduleRequests.length > before ? state : null;
  }, 'empty account schedule could not be saved');
  result.emptySchedule = saved.scheduleRequests.at(-1);
  document.querySelector('.strategy-grid').scrollIntoView({ block: 'start' });
}

async function waitFor(predicate, message, timeout = 5000) {
  const deadline = performance.now() + timeout;
  while (performance.now() < deadline) {
    const value = await predicate();
    if (value) return value;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error(message);
}

async function fixtureState() {
  const response = await fetch('/__browser-state');
  return response.json();
}

async function waitForPanel() {
  await waitFor(
    () => document.querySelector('#connection[data-state="ready"]') && document.querySelector('[data-account-selection]'),
    'panel did not load account state',
  );
}

async function checkResponsiveLayout(result) {
  await waitForPanel();
  const rect = (selector) => {
    const element = document.querySelector(selector);
    if (!element) return null;
    const box = element.getBoundingClientRect();
    const style = getComputedStyle(element);
    return { width: box.width, height: box.height, left: box.left, right: box.right, display: style.display, hidden: element.hidden };
  };
  const accountRow = document.querySelector('#accounts-table tr');
  const accountSelection = document.querySelector('[data-account-selection]');
  const accountText = accountRow?.textContent || '';
  const accountViewport = rect('.table-wrap');
  const settingsPanel = rect('#settings');
  const simulatorPanel = rect('.simulator-panel');
  const historyPanel = rect('.history-panel');
  const controls = [rect('[data-action="refresh-quota"]'), rect('[data-action="run-probe"]')];
  result.viewport = { innerWidth: innerWidth, innerHeight: innerHeight };
  result.overflow = {
    htmlScrollWidth: document.documentElement.scrollWidth,
    htmlClientWidth: document.documentElement.clientWidth,
    bodyScrollWidth: document.body.scrollWidth,
    bodyClientWidth: document.body.clientWidth,
  };
  result.essential = {
    summary: Boolean(rect('#account-summary')?.width > 0 && rect('#summary-scheduled')?.width > 0),
    accounts: Boolean(rect('#accounts-table')?.width > 0 && accountRow && accountViewport?.width > 0 && accountViewport.right <= innerWidth + 1),
    accountState: Boolean(accountSelection && !accountSelection.disabled && accountText.includes('b***@example.com') && !accountText.includes('browser@example.com')),
    workspace: Boolean(settingsPanel?.width > 0 && simulatorPanel?.width > 0 && historyPanel?.width > 0),
    controls: controls.every((control) => control && control.width > 0 && control.height >= 36 && control.left >= -1 && control.right <= innerWidth + 1),
  };
}

async function checkScheduleAndProbeContracts(result) {
  await waitForPanel();
  const action = document.querySelector('[data-account-selection]');
  const scheduled = document.querySelector('[data-account-scheduled]');
  if (!scheduled || scheduled.checked) throw new Error('account unexpectedly starts scheduled');
  action.click();
  if (!action.checked || scheduled.checked) throw new Error('action selection changed scheduled membership');
  scheduled.click();
  document.querySelector('[name="preheat_lead_minutes"]').value = '30';
  document.querySelector('[name="preheat_span_minutes"]').value = '15';
  document.querySelector('#schedule-form button[type="submit"]').click();
  const saved = await waitFor(async () => { const value = await fixtureState(); return value.scheduleRequests.length ? value : null; }, 'schedule request was not sent');
  result.scheduleRequest = saved.scheduleRequests.at(-1);

  const before = await fixtureState();
  document.querySelector('[data-action="run-probe"]').click();
  await waitFor(() => document.querySelector('#probe-dialog').open, 'probe consent dialog did not open');
  const unconfirmed = await fixtureState();
  if (unconfirmed.probeRequests.length !== before.probeRequests.length) throw new Error('probe request was sent before confirmation');
  document.querySelector('#probe-confirm').click();
  const probed = await waitFor(async () => { const value = await fixtureState(); return value.probeRequests.length > before.probeRequests.length ? value : null; }, 'confirmed probe request was not sent');
  result.probeRequest = probed.probeRequests.at(-1);
}
