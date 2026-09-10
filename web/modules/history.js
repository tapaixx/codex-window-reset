const OUTCOME_LABELS = {
  succeeded: 'Succeeded',
  unauthorized: 'Unauthorized',
  forbidden: 'Forbidden',
  payment_required: 'Payment required',
  rate_limited: 'Rate limited',
  upstream_error: 'Upstream error',
  network_error: 'Network error',
  timeout: 'Timeout',
  response_error: 'Response error',
  unexpected_output: 'Unexpected output',
  credential_error: 'Credential error',
  disabled: 'Disabled',
  verified_started: 'Verified started',
  already_active: 'Already active',
  unchanged: 'Unchanged',
  unverified: 'Unverified',
  not_observed: 'Not observed',
};

function node(tag, text = '', className = '') {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== '') element.textContent = text;
  return element;
}

function dateLabel(value) {
  if (!value) return 'Not recorded';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'Not recorded' : date.toLocaleString(undefined, { dateStyle: 'short', timeStyle: 'short' });
}

function masked(value, fallback) {
  const text = String(value || fallback || 'Account');
  return text.includes('*') ? text : `${text.slice(0, 1)}***`;
}

function tableCell(label, content) {
  const cell = node('td', '', 'record-cell');
  cell.dataset.label = label;
  if (typeof content === 'string') cell.textContent = content;
  else if (content) cell.append(content);
  return cell;
}

export function renderHistory(container, records = []) {
  container.replaceChildren();
  const visible = records.slice(0, 100);
  if (!visible.length) {
    container.append(node('p', 'No operation records.', 'empty-state'));
    return;
  }
  const wrapper = node('div', '', 'record-table-wrap');
  const table = document.createElement('table');
  table.className = 'record-table';
  const head = document.createElement('thead');
  const headerRow = document.createElement('tr');
  for (const label of ['Time', 'Account', 'Trigger', 'Request outcome', 'Window outcome', 'HTTP / latency', 'Error']) headerRow.append(node('th', label));
  head.append(headerRow);
  const body = document.createElement('tbody');
  for (const record of visible) {
    const row = document.createElement('tr');
    row.append(
      tableCell('Time', dateLabel(record.finished_at || record.started_at)),
      tableCell('Account', masked(record.masked_identity, record.account_key)),
      tableCell('Trigger', String(record.trigger || 'operation')),
      tableCell('Request outcome', OUTCOME_LABELS[record.request_outcome] || String(record.request_outcome || 'Unknown')),
      tableCell('Window outcome', OUTCOME_LABELS[record.window_outcome] || String(record.window_outcome || 'Unknown')),
      tableCell('HTTP / latency', `${record.http_status || '-'} / ${record.latency_ms || 0} ms`),
      tableCell('Error', String(record.error_code || 'None')),
    );
    body.append(row);
  }
  table.append(head, body);
  wrapper.append(table);
  container.append(wrapper);
}

export function renderResetAudit(container, records = []) {
  container.replaceChildren();
  if (!records.length) {
    container.append(node('p', 'No reset audit records.', 'empty-state'));
    return;
  }
  const wrapper = node('div', '', 'record-table-wrap');
  const table = document.createElement('table');
  table.className = 'record-table audit-table';
  const head = document.createElement('thead');
  const headerRow = document.createElement('tr');
  for (const label of ['Requested', 'Account', 'Credits before', 'Outcome', 'HTTP category', 'Correlation']) headerRow.append(node('th', label));
  head.append(headerRow);
  const body = document.createElement('tbody');
  for (const record of records) {
    const row = document.createElement('tr');
    row.append(
      tableCell('Requested', dateLabel(record.requested_at)),
      tableCell('Account', masked(record.masked_identity, record.account_key)),
      tableCell('Credits before', record.prior_applicable_credits === undefined ? 'Unknown' : String(record.prior_applicable_credits)),
      tableCell('Outcome', String(record.outcome || 'Unknown')),
      tableCell('HTTP category', String(record.http_category || 'Not reported')),
      tableCell('Correlation', String(record.correlation_id || 'Not reported')),
    );
    body.append(row);
  }
  table.append(head, body);
  wrapper.append(table);
  container.append(wrapper);
}
