function node(tag, text = '', className = '') {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== '') element.textContent = text;
  return element;
}

function number(value) {
  return Number.isFinite(Number(value)) ? Number(value) : 0;
}

function timeLabel(value) {
  if (!value) return '--:--';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '--:--' : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function metric(label, value, unit = 'minutes') {
  const item = node('div', '', 'simulation-metric');
  item.append(node('span', label, 'metric-label'), node('strong', `${value} ${unit}`, 'metric-value'));
  return item;
}

function timelineSegment(segment) {
  const start = new Date(segment.start);
  const end = new Date(segment.end);
  const startMinutes = Number.isNaN(start.getTime()) ? 0 : start.getUTCHours() * 60 + start.getUTCMinutes();
  const endMinutes = Number.isNaN(end.getTime()) ? startMinutes + 1 : end.getUTCHours() * 60 + end.getUTCMinutes();
  const duration = Math.max(1, endMinutes - startMinutes);
  const item = node('li', '', `timeline-segment timeline-${segment.kind || 'idle'}`);
  item.style.setProperty('--segment-start', `${Math.max(0, Math.min(1440, startMinutes)) / 14.4}%`);
  item.style.setProperty('--segment-width', `${Math.max(0.6, Math.min(100, duration / 14.4))}%`);
  item.setAttribute('aria-label', `${segment.kind || 'idle'} ${timeLabel(segment.start)} to ${timeLabel(segment.end)}`);
  item.append(node('span', segment.kind || 'idle'), node('small', `${timeLabel(segment.start)} - ${timeLabel(segment.end)}`));
  return item;
}

export function renderSimulation(container, result = {}, observedQuota = []) {
  container.replaceChildren();
  if (!result || Object.keys(result).length === 0) {
    container.append(node('p', 'Run a simulation to see the 24-hour operating timeline.', 'empty-state'));
    return;
  }

  const metrics = node('div', '', 'simulation-metrics');
  metrics.append(
    metric('Available Coverage', number(result.scheduled?.available_coverage_minutes)),
    metric('Idle Window', number(result.scheduled?.idle_window_minutes)),
    metric('Net Gain', number(result.net_gain_minutes)),
  );
  container.append(metrics);

  const assumptions = node('section', '', 'simulation-section');
  assumptions.append(node('h3', 'Assumptions'));
  const assumptionList = node('dl', '');
  const entries = Object.entries(result.assumptions || {});
  if (!entries.length) assumptionList.append(node('p', 'No additional assumptions returned.'));
  for (const [key, value] of entries) {
    assumptionList.append(node('dt', key.replaceAll('_', ' ')), node('dd', String(value)));
  }
  assumptions.append(assumptionList);
  container.append(assumptions);

  const observed = node('section', '', 'simulation-section');
  observed.append(node('h3', 'Observed quota'));
  const observedList = node('ul', '', 'compact-list');
  if (!observedQuota.length) observedList.append(node('li', 'No quota snapshot loaded. Refresh an account explicitly to compare observed values.'));
  for (const item of observedQuota) {
    const accountKey = item?.snapshot?.account_key || item?.account_key || 'unknown account';
    const stale = item?.stale ? 'stale' : 'current';
    observedList.append(node('li', `${accountKey}: ${stale}`));
  }
  observed.append(observedList);
  container.append(observed);

  const timeline = node('section', '', 'simulation-section');
  timeline.append(node('h3', '24-hour timeline'));
  const axis = node('div', '', 'timeline-axis');
  axis.append(node('span', '00:00'), node('span', '06:00'), node('span', '12:00'), node('span', '18:00'), node('span', '24:00'));
  const list = node('ol', '', 'timeline');
  for (const segment of result.timeline_segments || []) list.append(timelineSegment(segment));
  if (!list.children.length) list.append(node('li', 'No planned segments returned.', 'empty-state'));
  timeline.append(axis, list);
  container.append(timeline);
}
