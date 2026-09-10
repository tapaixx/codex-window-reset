export function renderAccounts(container, accounts = []) {
  container.replaceChildren();
  if (accounts.length === 0) {
    const row = document.createElement('tr');
    row.innerHTML = '<td colspan="4">No accounts discovered.</td>';
    container.append(row);
    return;
  }
  for (const account of accounts) {
    const row = document.createElement('tr');
    row.innerHTML = `<th scope="row">${escapeText(account.masked_identity ?? account.account_key)}</th><td>${escapeText(account.plan_label ?? '')}</td><td>${account.disabled ? 'Disabled' : account.unavailable ? 'Unavailable' : 'Available'}</td><td>${account.scheduled ? 'Yes' : 'No'}</td>`;
    container.append(row);
  }
}

function escapeText(value) {
  return String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;').replaceAll("'", '&#039;');
}
