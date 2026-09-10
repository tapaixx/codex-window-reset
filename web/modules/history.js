export function renderHistory(container, records = []) {
  container.replaceChildren();
  for (const record of records) {
    const item = document.createElement('div');
    item.className = 'record';
    item.textContent = `${record.trigger ?? 'operation'} · ${record.account_key ?? ''} · ${record.request_outcome ?? ''}`;
    container.append(item);
  }
  if (records.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'record';
    empty.textContent = 'No records.';
    container.append(empty);
  }
}
