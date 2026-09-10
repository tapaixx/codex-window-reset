function node(tag, text = '', className = '') {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== '') element.textContent = text;
  return element;
}

function number(value) {
  return Number.isFinite(Number(value)) ? Number(value) : 0;
}

function percent(value) {
  return Math.max(0, Math.min(100, number(value)));
}

function count(value) {
  if (value === null || value === undefined || value === '') return null;
  return Number.isFinite(Number(value)) ? Math.max(0, Math.floor(Number(value))) : null;
}

function timeParts(value, timezone) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  const options = {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  };
  try {
    const parts = new Intl.DateTimeFormat('en-GB', options).formatToParts(date);
    return Object.fromEntries(parts.filter((part) => ['year', 'month', 'day', 'hour', 'minute'].includes(part.type)).map((part) => [part.type, Number(part.value)]));
  } catch {
    const parts = new Intl.DateTimeFormat('en-GB', { ...options, timeZone: 'UTC' }).formatToParts(date);
    return Object.fromEntries(parts.filter((part) => ['year', 'month', 'day', 'hour', 'minute'].includes(part.type)).map((part) => [part.type, Number(part.value)]));
  }
}

function calendarDay(parts) {
  return Date.UTC(parts.year, parts.month - 1, parts.day) / 86400000;
}

export function timelineSegmentClock(segment, timezone = 'Asia/Shanghai') {
  const start = timeParts(segment?.start, timezone);
  const end = timeParts(segment?.end, timezone);
  const startMinutes = start ? start.hour * 60 + start.minute : 0;
  const dayOffset = start && end ? calendarDay(end) - calendarDay(start) : 0;
  const endMinutes = end ? dayOffset * 1440 + end.hour * 60 + end.minute : startMinutes + 1;
  return {
    startMinutes,
    endMinutes: endMinutes > startMinutes ? endMinutes : startMinutes + 1,
    startLabel: start ? `${String(start.hour).padStart(2, '0')}:${String(start.minute).padStart(2, '0')}` : '--:--',
    endLabel: end ? `${String(end.hour).padStart(2, '0')}:${String(end.minute).padStart(2, '0')}` : '--:--',
  };
}

export function formatObservedQuotaLine(item = {}) {
  const snapshot = item.snapshot || {};
  const accountKey = snapshot.account_key || item.account_key || 'unknown account';
  const status = item.refresh_error_code ? 'refresh failed' : item.stale ? 'stale' : 'current';
  const windows = (Array.isArray(snapshot.windows) ? snapshot.windows : []).map((window) => {
    const kind = window.short ? 'Short' : 'Long';
    return `${kind} ${Math.max(0, number(window.duration_minutes))} min: ${percent(window.remaining_percent)}%`;
  });
  const resetCredits = count(snapshot.reset_applicable_count) ?? (snapshot.reset_info_complete ? (Array.isArray(snapshot.reset_credits) ? snapshot.reset_credits.length : 0) : 'unknown');
  return `${accountKey}: ${status}; ${windows.join(', ') || 'No observed windows'}; applicable reset credits: ${resetCredits}`;
}

function metric(label, value, unit = 'minutes') {
  const item = node('div', '', 'simulation-metric');
  item.append(node('span', label, 'metric-label'), node('strong', `${value} ${unit}`, 'metric-value'));
  return item;
}

function timelineSegment(segment, timezone) {
  const clock = timelineSegmentClock(segment, timezone);
  const duration = Math.max(1, clock.endMinutes - clock.startMinutes);
  const item = node('li', '', `timeline-segment timeline-${segment.kind || 'idle'}`);
  item.style.setProperty('--segment-start', `${Math.max(0, Math.min(1440, clock.startMinutes)) / 14.4}%`);
  item.style.setProperty('--segment-width', `${Math.max(0.6, Math.min(100, duration / 14.4))}%`);
  item.setAttribute('aria-label', `${segment.kind || 'idle'} ${clock.startLabel} to ${clock.endLabel}`);
  item.append(node('span', segment.kind || 'idle'), node('small', `${clock.startLabel} - ${clock.endLabel}`));
  return item;
}

export function renderSimulation(container, result = {}, observedQuota = [], timezone = 'Asia/Shanghai') {
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
  for (const item of observedQuota) observedList.append(node('li', formatObservedQuotaLine(item)));
  observed.append(observedList);
  container.append(observed);

  const timeline = node('section', '', 'simulation-section');
  timeline.append(node('h3', '24-hour timeline'));
  const axis = node('div', '', 'timeline-axis');
  axis.append(node('span', '00:00'), node('span', '06:00'), node('span', '12:00'), node('span', '18:00'), node('span', '24:00'));
  const list = node('ol', '', 'timeline');
  for (const segment of result.timeline_segments || []) list.append(timelineSegment(segment, timezone));
  if (!list.children.length) list.append(node('li', 'No planned segments returned.', 'empty-state'));
  timeline.append(axis, list);
  container.append(timeline);
}
