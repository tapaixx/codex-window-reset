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
    if (mode === 'reset') await checkResetConfirmation(result);
    else if (mode === 'contracts') await checkScheduleAndProbeContracts(result);
    else await checkResponsiveLayout(result);
    result.ok = true;
  } catch (error) {
    result.error = `${error?.stack || String(error)}${result.clientErrors.length ? `\nClient errors:\n${result.clientErrors.join('\n')}` : ''}`;
  }
  output.textContent = JSON.stringify(result);
})();

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

async function checkResetConfirmation(result) {
  await waitForPanel();
  const initial = await fixtureState();
  const selection = document.querySelector('[data-account-selection]');
  selection.click();
  await waitFor(() => selection.checked, 'account selection did not update');

  document.querySelector('[data-action="refresh-quota"]').click();
  await waitFor(async () => (await fixtureState()).quotaRefreshRequests.length > initial.quotaRefreshRequests.length, 'quota refresh request was not sent');
  await waitFor(() => !document.querySelector('[data-row-action="reset"]').disabled, 'reset action did not become eligible');

  document.querySelector('[data-row-action="reset"]').click();
  await waitFor(() => document.querySelector('#reset-dialog').open, 'reset confirmation dialog did not open');
  result.dialogAccount = document.querySelector('#reset-account').textContent;
  result.dialogCredits = document.querySelector('#reset-credits').textContent;
  await new Promise((resolve) => setTimeout(resolve, 100));
  const beforeCancel = await fixtureState();
  result.cancelledRequestCount = beforeCancel.resetRequests.length;
  if (result.cancelledRequestCount !== initial.resetRequests.length) throw new Error('reset was sent before confirmation');

  document.querySelector('[data-action="close-reset"]').click();
  await waitFor(() => !document.querySelector('#reset-dialog').open, 'reset dialog did not close');
  const afterCancel = await fixtureState();
  if (afterCancel.resetRequests.length !== initial.resetRequests.length) throw new Error('cancel sent a reset request');

  document.querySelector('[data-row-action="reset"]').click();
  await waitFor(() => document.querySelector('#reset-dialog').open, 'reset confirmation dialog did not reopen');
  document.querySelector('#reset-confirm').click();
  const final = await waitFor(async () => {
    const state = await fixtureState();
    return state.resetRequests.length === initial.resetRequests.length + 1 ? state : null;
  }, 'confirmed reset request was not sent');
  result.resetRequest = final.resetRequests.at(-1);
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
