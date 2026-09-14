// Reference layout: codex-health-monitor's simulator (MIT; see NOTICE).
// Coverage is supplied by the Go simulator; this module only presents it.
const simulationKinds = { available: '预计可用', limited: '预计受限', break: '午休 / 非工作间隔', idle: '非工作时间' };

function simElement(tag, content = '', className = '') {
  const element = document.createElement(tag);
  element.className = className;
  if (content !== '') element.textContent = content;
  return element;
}

function simDuration(value) {
  const minutes = Math.max(0, Math.round(Number(value) || 0));
  const hours = Math.floor(minutes / 60);
  return hours ? `${hours}小时${minutes % 60 ? `${minutes % 60}分` : ''}` : `${minutes}分钟`;
}

function simClock(value, timezone) {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return null;
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-GB', {
    timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  }).formatToParts(date).map((part) => [part.type, part.value]));
  return {
    label: `${parts.hour}:${parts.minute}`,
    minute: Number(parts.hour) * 60 + Number(parts.minute) + Number(parts.second) / 60,
    date: `${parts.year}-${parts.month}-${parts.day}`,
  };
}

export function simulationRange(start, end, timezone, domain = { start: 0, end: 1440 }) {
  if (!start || !end || Date.parse(end) <= Date.parse(start)) return null;
  const from = simClock(start, timezone), to = simClock(end, timezone);
  if (!from || !to) return null;
  const last = to.date > from.date ? 1440 : to.minute;
  if (last <= from.minute) return null;
  const firstVisible = Math.max(from.minute, domain.start), lastVisible = Math.min(last, domain.end);
  if (lastVisible <= firstVisible) return null;
  return { left: (firstVisible - domain.start) / (domain.end - domain.start) * 100, width: (lastVisible - firstVisible) / (domain.end - domain.start) * 100, label: `${from.label}–${last === 1440 ? '24:00' : to.label}` };
}

export function simulationDomain(result = {}, config = {}) {
  const timezone = config.timezone || 'Asia/Shanghai';
  const minutes = [];
  for (const period of config.work_periods || []) {
    for (const clock of [period.start, period.end]) {
      if (!/^\d{2}:\d{2}$/.test(clock || '')) continue;
      const [hour, minute] = clock.split(':').map(Number);
      if (hour < 24 && minute < 60) minutes.push(hour * 60 + minute);
    }
  }
  const intervals = [...(result.baseline?.timeline_segments || []), ...(result.scheduled?.timeline_segments || [])].filter((segment) => segment.kind !== 'idle');
  for (const window of result.preheat_windows || []) {
    if (!window.missed) intervals.push({ start: window.window_start, end: window.window_end });
  }
  for (const interval of intervals) {
    const range = simulationRange(interval.start, interval.end, timezone);
    if (range) minutes.push(Math.round(range.left * 14.4), Math.round((range.left + range.width) * 14.4));
  }
  if (!minutes.length) return { start: 0, end: 1440 };
  return { start: Math.max(0, Math.floor(Math.min(...minutes) / 60) * 60 - 60), end: Math.min(1440, Math.ceil(Math.max(...minutes) / 60) * 60 + 60) };
}

const axisPercent = (minute, domain) => (minute - domain.start) / (domain.end - domain.start) * 100;
const axisClock = (minute) => `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`;

function simPosition(element, range) {
  element.style.setProperty('--start', `${range.left}%`);
  element.style.setProperty('--width', `${range.width}%`);
}

function simMetric(label, value, detail, tone = '') {
  const card = simElement('div', '', `sim-metric ${tone}`);
  card.append(simElement('span', label), simElement('strong', value), simElement('small', detail));
  return card;
}

// No pass/fail badge here. The former 达标/未达标 verdict compared work-time
// coverage against `health_threshold_percent`, a field the scheduler never
// reads, at a default (80%) the default budget cannot reach — so it was
// permanently red and carried no information. Coverage is stated as a number.
function simStrategyCard(key, title, description, metrics, workMinutes) {
  const ratio = workMinutes > 0 ? Math.max(0, Math.min(100, metrics.available_coverage_minutes / workMinutes * 100)) : 0;
  const card = simElement('article', '', `strategy-card strategy-${key}`);
  const heading = simElement('h3');
  heading.append(document.createTextNode(title), simElement('small', description, 'strategy-sub'));
  const value = simElement('div', '', 'strategy-value');
  value.append(simElement('strong', simDuration(metrics.available_coverage_minutes)), simElement('span', '预计可用'));
  const status = simElement('div', '', 'strategy-health');
  status.append(simElement('span', workMinutes === 0 ? '未排班' : `覆盖工作时段 ${ratio.toFixed(1)}%`, 'coverage-status'));
  const meter = simElement('meter', '', 'coverage-meter');
  meter.min = 0; meter.max = 100; meter.value = ratio;
  meter.setAttribute('aria-label', `${title} 工作时段覆盖率 ${ratio.toFixed(1)}%`);
  const detail = simElement('p', `窗口空闲 ${simDuration(metrics.idle_window_minutes)}（不计入工作覆盖）`);
  card.append(heading, value, status, meter, detail);
  return card;
}

function simPreheatGroups(occurrences, timezone) {
  const groups = new Map();
  for (const item of occurrences || []) {
    if (item.missed || !item.planned_at || !simulationRange(item.window_start, item.window_end, timezone)) continue;
    const point = simClock(item.planned_at, timezone);
    if (!point) continue;
    const key = `${item.window_start}/${item.window_end}`;
    if (!groups.has(key)) groups.set(key, { start: item.window_start, end: item.window_end, points: [] });
    groups.get(key).points.push(point);
  }
  return [...groups.values()].sort((a, b) => Date.parse(a.start) - Date.parse(b.start)).map((group) => {
    group.points.sort((a, b) => a.minute - b.minute);
    const first = group.points[0], last = group.points.at(-1);
    return { ...group, first, execution: first.label === last.label ? first.label : `${first.label}–${last.label}` };
  });
}

function simTimeline(result, timezone, groups, config = {}) {
  const domain = simulationDomain(result, config);
  const card = simElement('section', '', 'timeline-block');
  const header = simElement('div', '', 'timeline-title');
  header.append(simElement('h3', '工作日时间轴'), simElement('span', `${timezone} · ${axisClock(domain.start)}–${axisClock(domain.end)}`));
  const legend = simElement('div', '', 'timeline-legend');
  for (const [kind, name] of [['available', '预计可用'], ['limited', '预计受限'], ['break', '午休'], ['preheat', '预热时间点']]) {
    const item = simElement('span');
    const swatch = simElement('i', '', `legend-${kind}`); swatch.setAttribute('aria-hidden', 'true');
    item.append(swatch, document.createTextNode(name)); legend.append(item);
  }
  const scroll = simElement('div', '', 'timeline-scroll');
  scroll.tabIndex = 0; scroll.setAttribute('role', 'region'); scroll.setAttribute('aria-label', 'A/B 时间轴，小屏可左右滚动');
  const chart = simElement('div', '', 'timeline-chart');
  chart.style.setProperty('--grid-step', `${60 / (domain.end - domain.start) * 100}%`);
  const ruler = simElement('div', '', 'timeline-ruler');
  const tickStep = domain.end - domain.start > 960 ? 120 : 60;
  const tickMinutes = [];
  for (let minute = domain.start; minute <= domain.end - tickStep; minute += tickStep) tickMinutes.push(minute);
  tickMinutes.push(domain.end);
  for (const minute of tickMinutes) {
    const tick = simElement('span', axisClock(minute));
    tick.style.setProperty('--at', `${axisPercent(minute, domain)}%`); ruler.append(tick);
  }
  chart.append(ruler);
  const boundaries = simElement('div', '', 'work-boundaries'); boundaries.setAttribute('aria-hidden', 'true');
  const period = (config.work_periods || [])[0] || {};
  for (const [label, value] of [['上班', period.start], ['下班', (config.work_periods || []).at(-1)?.end]]) {
    if (!value) continue;
    const [hours, minutes] = String(value).split(':').map(Number);
    const marker = simElement('span', label, 'work-boundary');
    marker.style.setProperty('--at', `${axisPercent(hours * 60 + minutes, domain)}%`);
    marker.title = `${label} ${value}`;
    boundaries.append(marker);
  }
  chart.append(boundaries);
  for (const [letter, key, description] of [['不预热', 'baseline', '对照基准'], ['按计划预热', 'scheduled', '已配置计划']]) {
    const row = simElement('div', '', `timeline-row strategy-${letter.toLowerCase()}`);
    const label = simElement('div', '', 'timeline-row-label');
    label.append(simElement('strong', letter), simElement('small', description));
    const lane = simElement('div', '', 'timeline-lane');
    lane.setAttribute('aria-label', `${letter} 的工作覆盖与预热时间`);
    for (const segment of result[key].timeline_segments) {
      if (!simulationKinds[segment.kind] || segment.kind === 'idle') continue;
      const range = simulationRange(segment.start, segment.end, timezone, domain);
      if (!range) continue;
      const band = simElement('span', '', `timeline-band ${segment.kind}-band`);
      band.dataset.kind = segment.kind;
      const title = `${simulationKinds[segment.kind]} ${range.label}`;
      band.title = title; band.setAttribute('aria-label', title);
      simPosition(band, range);
      if (range.width >= 8) band.append(simElement('span', segment.kind === 'break' ? '午休' : simulationKinds[segment.kind]));
      lane.append(band);
    }
    if (key === 'scheduled') groups.forEach((group, index) => {
      const range = simulationRange(group.start, group.end, timezone, domain);
      if (!range) return;
      const window = simElement('span', '', 'preheat-range');
      simPosition(window, range); window.title = `预热时段 ${index + 1} · ${range.label}`;
      lane.append(window);
      const point = simElement('button', '', 'preheat-point');
      point.type = 'button'; point.dataset.preheatMarker = String(index + 1);
      point.style.setProperty('--at', `${axisPercent(group.first.minute, domain)}%`);
      const detail = `预热 ${index + 1}：允许时段 ${range.label}，计划执行 ${group.execution}`;
      point.setAttribute('aria-label', detail); point.title = detail;
      point.append(simElement('span', `${group.first.label}`, 'preheat-label'));
      const tooltip = simElement('span', detail, 'preheat-tooltip'); tooltip.setAttribute('aria-hidden', 'true'); point.append(tooltip);
      lane.append(point);
    });
    row.append(label, lane); chart.append(row);
  }
  scroll.append(chart);
  card.append(header, legend, scroll, simElement('p', '色块表示工作时段的预计覆盖；虚线框表示允许预热的时段，圆点标出首个计划执行时间。', 'timeline-caption'));

  // Only strategy B is listed. Strategy A is the no-preheat control line whose
  // segments exist to make the timeline comparison readable; repeating them as
  // rows invited reading the control group as the configured plan.
  const details = simElement('details', '', 'timeline-details');
  details.append(simElement('summary', '查看分段时间明细'));
  const table = simElement('table');
  const caption = simElement('caption', `按计划预热 · 工作覆盖明细 · ${timezone}`); table.append(caption);
  const head = simElement('thead'); const headings = simElement('tr');
  for (const text of ['时间', '状态']) { const th = simElement('th', text); th.scope = 'col'; headings.append(th); }
  head.append(headings); table.append(head);
  const body = simElement('tbody');
  for (const segment of result.scheduled?.timeline_segments || []) {
    if (segment.kind === 'idle') continue;
    const range = simulationRange(segment.start, segment.end, timezone);
    if (!range) continue;
    const row = simElement('tr');
    for (const value of [range.label, simulationKinds[segment.kind] || '未知']) row.append(simElement('td', value));
    body.append(row);
  }
  table.append(body); details.append(table); card.append(details);
  return card;
}

export function renderSimulationComparison(root, result, config) {
  root.replaceChildren();
  const timezone = config.timezone || 'Asia/Shanghai';
  const work = Number(result.work_minutes) || 0;
  const gain = Number(result.net_gain_minutes) || 0;
  const metrics = simElement('div', '', 'sim-metrics');
  metrics.append(simMetric('当天总工作时长', simDuration(work), '扣除午休 / 非工作间隔', 'work'),
    simMetric('不预热可用', simDuration(result.baseline?.available_coverage_minutes), '对照：上班后首次使用才开窗'),
    simMetric('按计划预热可用', simDuration(result.scheduled?.available_coverage_minutes), '按已配置的预热计划', 'scheduled'),
    simMetric('预热后增益', `${gain === 0 ? '' : gain < 0 ? '−' : '+'}${simDuration(Math.abs(gain))}`, gain === 0 ? '当前参数下无额外收益' : '按计划预热相对不预热的差值', gain === 0 ? 'neutral' : gain < 0 ? 'loss' : 'gain'));
  root.append(metrics);
  const comparison = simElement('div', '', 'strategy-compare');
  comparison.append(simStrategyCard('a', '不预热（对照）', '上班后第一次使用才开启窗口', result.baseline || {}, work),
    simStrategyCard('b', '按计划预热', '上班前先使用自动预热', result.scheduled || {}, work));
  root.append(comparison);
  const groups = simPreheatGroups(result.preheat_windows, timezone);
  const overview = simElement('section', '', 'window-overview');
  const title = simElement('div', '', 'window-overview-title');
  title.append(simElement('h3', '预热时段概览'), simElement('span', '来自服务端调度器'));
  const strip = simElement('div', '', 'window-strip');
  groups.forEach((group, index) => {
    const card = simElement('div', '', 'window-pill');
    card.append(simElement('span', `预热时段 ${index + 1}`),
      simElement('strong', simulationRange(group.start, group.end, timezone).label),
      simElement('small', `计划执行 ${group.execution}`));
    strip.append(card);
  });
  if (!groups.length) strip.append(simElement('p', work ? '当前配置没有可执行的预热时段。' : '模拟日期不是工作日，当前没有工作覆盖或预热计划。', 'simulation-empty'));
  overview.append(title, strip); root.append(overview);
  if (Array.isArray(result.baseline?.timeline_segments) && Array.isArray(result.scheduled?.timeline_segments)) {
    root.append(simTimeline(result, timezone, groups, config));
  } else {
    root.append(simElement('p', '服务端未返回策略分段，请确认插件已更新。', 'simulation-empty'));
  }
  const scope = config.scheduled_account_keys?.length ? `已配置 ${config.scheduled_account_keys.length} 个自动预热账户。` : '账户集合为空：仅展示示例计划，不触发自动请求。';
  root.append(simElement('p', `${scope} 单账户示例：按计划预热按最早有效预热起点推演；无提前预热则与对照相同，多账户额度不叠加。单窗口预计可用 ${Number(result.assumptions?.productivity_minutes) || 0} 分钟，仅在工作时段消耗，午休不消耗；按窗口周期恢复，后续预热请求不视为强制重置。模拟不发送真实请求，也不读取当前配额。`, 'simulation-assumptions'));
}
