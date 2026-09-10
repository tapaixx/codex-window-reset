import { request, requestErrorMessage } from './api.js';
import { createStore } from './state.js';
import { renderAccounts } from './accounts.js';
import { readScheduleDraft, renderSchedule } from './schedule.js';
import { renderHistory } from './history.js';

const store = createStore({ loading: true, accounts: [], history: [], schedule: {}, draft: {}, status: {} });
const elements = {
  connection: document.querySelector('#connection'),
  summary: document.querySelector('#status-summary'),
  accounts: document.querySelector('#accounts-table'),
  schedule: document.querySelector('#schedule-form'),
  history: document.querySelector('#history-list'),
  feedback: document.querySelector('#feedback'),
};

store.subscribe(render);

async function loadPanel() {
  try {
    const [status, accounts, schedule] = await Promise.all([
      request('/status'),
      request('/accounts'),
      request('/schedule'),
    ]);
    store.dispatch({ type: 'status-loaded', value: status });
    store.dispatch({ type: 'accounts-loaded', value: accounts });
    store.dispatch({ type: 'schedule-loaded', value: schedule });
    elements.connection.dataset.state = 'ready';
    elements.connection.textContent = 'Connected';
  } catch (error) {
    elements.connection.dataset.state = 'error';
    elements.connection.textContent = 'Unavailable';
    store.dispatch({ type: 'feedback', value: requestErrorMessage(error) });
  }
}

async function loadHistory() {
  try {
    store.dispatch({ type: 'history-loaded', value: await request('/history') });
  } catch (error) {
    store.dispatch({ type: 'feedback', value: requestErrorMessage(error) });
  }
}

async function saveSchedule() {
  try {
    const draft = readScheduleDraft(elements.schedule, store.getState().draft);
    const value = await request('/schedule', { method: 'PUT', body: draft });
    store.dispatch({ type: 'schedule-loaded', value });
    store.dispatch({ type: 'feedback', value: 'Schedule saved.' });
  } catch (error) {
    store.dispatch({ type: 'feedback', value: requestErrorMessage(error) });
  }
}

function render(state) {
  const status = state.status ?? {};
  elements.summary.replaceChildren();
  for (const [label, value] of [['Enabled', status.enabled ? 'Yes' : 'No'], ['Run', status.run_id ?? 'Idle'], ['Completed', `${status.run_completed ?? 0}/${status.run_total ?? 0}`]]) {
    const item = document.createElement('div');
    item.className = 'summary-item';
    item.innerHTML = `<strong>${value}</strong><span>${label}</span>`;
    elements.summary.append(item);
  }
  renderAccounts(elements.accounts, state.accounts);
  renderSchedule(elements.schedule, state.draft, (name, value) => store.dispatch({ type: 'draft-changed', patch: { [name]: value } }));
  renderHistory(elements.history, state.history);
  elements.feedback.textContent = state.feedback ?? '';
}

document.querySelectorAll('[data-action]').forEach((button) => {
  button.addEventListener('click', () => {
    if (button.dataset.action === 'refresh') loadPanel();
    if (button.dataset.action === 'load-history') loadHistory();
    if (button.dataset.action === 'save') saveSchedule();
  });
});

loadPanel();
