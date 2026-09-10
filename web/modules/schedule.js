const WEEKDAYS = [
  ['1', 'Monday'], ['2', 'Tuesday'], ['3', 'Wednesday'], ['4', 'Thursday'],
  ['5', 'Friday'], ['6', 'Saturday'], ['7', 'Sunday'],
];

const NUMBER_FIELDS = new Set([
  'productivity_minutes', 'remaining_quota_floor_percent', 'remaining_window_floor_minutes',
  'long_window_floor_percent', 'probe_timeout_seconds', 'preheat_lead_minutes', 'preheat_span_minutes',
  'schema_version', 'revision',
]);

const CONFIG_KEYS = [
  'schema_version', 'revision', 'enabled', 'timezone', 'weekdays', 'work_periods',
  'preheat_lead_minutes', 'preheat_span_minutes', 'productivity_minutes',
  'remaining_quota_floor_percent', 'remaining_window_floor_minutes',
  'long_window_floor_percent', 'blackout_periods', 'probe_model',
  'probe_timeout_seconds', 'scheduled_account_keys',
];
const CONFIG_KEY_SET = new Set(CONFIG_KEYS);
const PERIOD_INPUT_NAME = /^(?:work_periods|blackout_periods)\[\d+\]\.(?:start|end)$/u;

function node(tag, text = '', className = '') {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== '') element.textContent = text;
  return element;
}

function labeledInput({ id, name, label, type = 'text', value = '', checked = false, required = false, min, max, step = 1 }) {
  const wrapper = node('div', '', 'field');
  const labelNode = node('label', label);
  labelNode.htmlFor = id;
  const input = document.createElement('input');
  input.id = id;
  input.name = name;
  input.type = type;
  input.value = value ?? '';
  input.checked = checked;
  input.required = required;
  if (min !== undefined) input.min = min;
  if (max !== undefined) input.max = max;
  if (type === 'number') input.step = step;
  input.dataset.configField = name;
  wrapper.append(labelNode, input);
  return { wrapper, input };
}

function periodEditor(kind, periods = [], onChange) {
  const wrapper = node('div', '', 'period-editor');
  const list = node('div', '', 'period-list');
  list.dataset.periodKind = kind;
  const values = periods.length ? periods : [{ start: '', end: '' }];
  values.forEach((period, index) => {
    const row = node('div', '', 'period-row');
    row.dataset.periodIndex = index;
    const start = labeledInput({
      id: `schedule-${kind}-${index}-start`,
      name: `${kind}[${index}].start`,
      label: 'Start time',
      type: 'time',
      value: period.start,
      required: kind === 'work_periods',
    });
    const end = labeledInput({
      id: `schedule-${kind}-${index}-end`,
      name: `${kind}[${index}].end`,
      label: 'End time',
      type: 'time',
      value: period.end,
      required: kind === 'work_periods',
    });
    const remove = node('button', 'Remove period', 'button button-secondary compact-button');
    remove.type = 'button';
    remove.dataset.removePeriod = kind;
    remove.addEventListener('click', () => {
      if (list.children.length <= 1) {
        start.input.value = '';
        end.input.value = '';
      } else {
        row.remove();
        renumberPeriods(list, kind);
      }
      onChange(kind, readPeriods(list));
    });
    row.append(start.wrapper, end.wrapper, remove);
    list.append(row);
  });
  const add = node('button', kind === 'work_periods' ? 'Add work period' : 'Add blackout period', 'button button-secondary');
  add.type = 'button';
  add.dataset.addPeriod = kind;
  add.addEventListener('click', () => {
    const index = list.children.length;
    const row = node('div', '', 'period-row');
    row.dataset.periodIndex = index;
    const start = labeledInput({ id: `schedule-${kind}-${index}-start`, name: `${kind}[${index}].start`, label: 'Start time', type: 'time' });
    const end = labeledInput({ id: `schedule-${kind}-${index}-end`, name: `${kind}[${index}].end`, label: 'End time', type: 'time' });
    const remove = node('button', 'Remove period', 'button button-secondary compact-button');
    remove.type = 'button';
    remove.addEventListener('click', () => { row.remove(); renumberPeriods(list, kind); onChange(kind, readPeriods(list)); });
    row.append(start.wrapper, end.wrapper, remove);
    list.append(row);
    onChange(kind, readPeriods(list));
  });
  wrapper.append(list, add);
  return wrapper;
}

function renumberPeriods(list, kind) {
  [...list.children].forEach((row, index) => {
    row.dataset.periodIndex = index;
    row.querySelectorAll('input').forEach((input) => {
      const suffix = input.name.endsWith('.start') ? 'start' : 'end';
      input.name = `${kind}[${index}].${suffix}`;
      input.id = `schedule-${kind}-${index}-${suffix}`;
      input.previousElementSibling.htmlFor = input.id;
    });
  });
}

function readPeriods(list) {
  return [...(list?.children || [])].map((row) => {
    const inputs = row.querySelectorAll('input');
    return { start: inputs[0]?.value || '', end: inputs[1]?.value || '' };
  }).filter((period) => period.start || period.end);
}

function normalizePeriods(periods) {
  return (Array.isArray(periods) ? periods : [])
    .map((period) => ({ start: String(period?.start || ''), end: String(period?.end || '') }))
    .filter((period) => period.start || period.end);
}

export function serializeScheduleDraft(input = {}) {
  const draft = {};
  for (const key of CONFIG_KEYS) {
    if (key === 'work_periods' || key === 'blackout_periods') {
      draft[key] = normalizePeriods(input[key]);
    } else if (Object.hasOwn(input, key)) {
      draft[key] = input[key];
    }
  }
  return draft;
}

function attachChange(input, name, onChange, container) {
  input.addEventListener('change', () => {
    const value = input.type === 'checkbox' ? input.checked : input.value;
    onChange(name, value);
  });
  input.addEventListener('input', () => {
    if (input.type !== 'checkbox') onChange(name, input.value);
    container.dispatchEvent(new CustomEvent('schedule-input', { detail: { name } }));
  });
}

export function renderSchedule(container, schedule = {}, onChange = () => {}) {
  container.replaceChildren();
  const config = schedule || {};
  const revisionMeta = node('p', `Server revision ${config.revision ?? 'not loaded'}`, 'form-meta');
  container.append(revisionMeta);

  const basics = node('fieldset', '', 'config-fieldset');
  basics.append(node('legend', 'Schedule configuration'));
  const schemaVersion = labeledInput({ id: 'schedule-schema-version', name: 'schema_version', label: 'Schema version', type: 'number', value: config.schema_version ?? '', min: 1 });
  schemaVersion.input.readOnly = true;
  const revisionField = labeledInput({ id: 'schedule-revision-field', name: 'revision', label: 'Server revision', type: 'number', value: config.revision ?? '', min: 0 });
  revisionField.input.readOnly = true;
  basics.append(schemaVersion.wrapper, revisionField.wrapper);
  const enabled = labeledInput({ id: 'schedule-enabled', name: 'enabled', label: 'Enable automatic preheat', type: 'checkbox', checked: Boolean(config.enabled) });
  basics.append(enabled.wrapper);
  const timezone = labeledInput({ id: 'schedule-timezone', name: 'timezone', label: 'Timezone', value: config.timezone || '', required: true });
  basics.append(timezone.wrapper);

  const weekdays = node('div', '', 'field weekday-field');
  weekdays.append(node('span', 'Work weekdays', 'field-label'));
  const weekdayOptions = node('div', '', 'choice-grid');
  const selectedWeekdays = new Set((config.weekdays || []).map(String));
  for (const [value, label] of WEEKDAYS) {
    const wrapper = node('label', '', 'choice');
    const input = document.createElement('input');
    input.type = 'checkbox';
    input.name = 'weekdays';
    input.value = value;
    input.checked = selectedWeekdays.has(value);
    input.setAttribute('aria-label', label);
    input.addEventListener('change', () => onChange('weekdays', readScheduleDraft(container, config).weekdays));
    wrapper.append(input, node('span', label));
    weekdayOptions.append(wrapper);
  }
  weekdays.append(weekdayOptions);
  basics.append(weekdays);

  const numericDefinitions = [
    ['productivity_minutes', 'Productivity minutes', config.productivity_minutes, 1],
    ['remaining_quota_floor_percent', 'Short window floor (%)', config.remaining_quota_floor_percent, 0],
    ['remaining_window_floor_minutes', 'Minimum remaining window (minutes)', config.remaining_window_floor_minutes, 1],
    ['long_window_floor_percent', 'Long window floor (%)', config.long_window_floor_percent, 0],
    ['preheat_lead_minutes', 'Preheat lead (minutes)', config.preheat_lead_minutes ?? '', 1],
    ['preheat_span_minutes', 'Preheat span (minutes)', config.preheat_span_minutes ?? '', 1],
  ];
  for (const [name, label, value, min] of numericDefinitions) {
    const field = labeledInput({ id: `schedule-${name}`, name, label, type: 'number', value: value ?? '', min, max: name.includes('percent') ? 100 : undefined });
    attachChange(field.input, name, onChange, container);
    basics.append(field.wrapper);
  }

  const scheduled = labeledInput({
    id: 'schedule-scheduled-account-keys',
    name: 'scheduled_account_keys',
    label: 'Scheduled account keys (comma separated)',
    value: (config.scheduled_account_keys || []).join(', '),
  });
  attachChange(scheduled.input, 'scheduled_account_keys', onChange, container);
  basics.append(scheduled.wrapper);
  container.append(basics);

  const work = node('fieldset', '', 'config-fieldset');
  work.append(node('legend', 'Work periods'));
  work.append(node('p', 'Suggestions only: 09:00-12:00 and 13:30-19:00. They are not defaults.', 'field-help'));
  work.append(periodEditor('work_periods', config.work_periods || [], onChange));
  container.append(work);

  const blackout = node('fieldset', '', 'config-fieldset');
  blackout.append(node('legend', 'Blackout periods'));
  blackout.append(node('p', 'Optional local periods in which no preheat should run.', 'field-help'));
  blackout.append(periodEditor('blackout_periods', config.blackout_periods || [], onChange));
  container.append(blackout);

  const advanced = document.createElement('details');
  advanced.className = 'advanced-settings';
  advanced.append(node('summary', 'Advanced probe settings'));
  const advancedGrid = node('div', '', 'advanced-grid');
  const model = labeledInput({ id: 'schedule-probe-model', name: 'probe_model', label: 'Probe model', value: config.probe_model || '', required: true });
  const timeout = labeledInput({ id: 'schedule-probe-timeout', name: 'probe_timeout_seconds', label: 'Probe timeout (seconds)', type: 'number', value: config.probe_timeout_seconds ?? '', min: 5, max: 120 });
  attachChange(model.input, 'probe_model', onChange, container);
  attachChange(timeout.input, 'probe_timeout_seconds', onChange, container);
  advancedGrid.append(model.wrapper, timeout.wrapper);
  advanced.append(advancedGrid);
  container.append(advanced);

  const note = node('p', 'Every preheat is a real Codex request and may consume ordinary quota or begin a Short Window.', 'disclosure');
  note.id = 'schedule-preheat-disclosure';
  container.append(note);

  attachChange(enabled.input, 'enabled', onChange, container);
  attachChange(timezone.input, 'timezone', onChange, container);
}

export function readScheduleDraft(container, original = {}) {
  const draft = serializeScheduleDraft(original);
  for (const input of container.querySelectorAll('[data-config-field]')) {
    const name = input.name;
    if (!CONFIG_KEY_SET.has(name) || PERIOD_INPUT_NAME.test(name)) continue;
    if (name === 'scheduled_account_keys') {
      draft[name] = input.value.split(',').map((key) => key.trim()).filter(Boolean);
    } else if (input.type === 'checkbox') {
      draft[name] = input.checked;
    } else if (NUMBER_FIELDS.has(name)) {
      draft[name] = input.value === '' ? null : Number(input.value);
    } else {
      draft[name] = input.value;
    }
  }
  draft.weekdays = [...container.querySelectorAll('input[name="weekdays"]:checked')].map((input) => Number(input.value));
  draft.work_periods = readPeriods(container.querySelector('[data-period-kind="work_periods"]'));
  draft.blackout_periods = readPeriods(container.querySelector('[data-period-kind="blackout_periods"]'));
  return serializeScheduleDraft(draft);
}

export function validateScheduleDraft(draft = {}) {
  const errors = [];
  if (!String(draft.timezone || '').trim()) errors.push({ field: 'timezone', message: 'Timezone is required.' });
  if (!String(draft.probe_model || '').trim()) errors.push({ field: 'probe_model', message: 'Probe model is required.' });
  if (draft.enabled) {
    if (!Number.isFinite(Number(draft.preheat_lead_minutes)) || Number(draft.preheat_lead_minutes) < 1) errors.push({ field: 'preheat_lead_minutes', message: 'Preheat lead is required before enabling the schedule.' });
    if (!Number.isFinite(Number(draft.preheat_span_minutes)) || Number(draft.preheat_span_minutes) < 1) errors.push({ field: 'preheat_span_minutes', message: 'Preheat span is required before enabling the schedule.' });
    if (!Array.isArray(draft.work_periods) || draft.work_periods.length === 0) errors.push({ field: 'work_periods', message: 'At least one work period is required before enabling the schedule.' });
    if (!Array.isArray(draft.scheduled_account_keys) || draft.scheduled_account_keys.length === 0) errors.push({ field: 'scheduled_account_keys', message: 'At least one scheduled account is required before enabling the schedule.' });
  }
  return errors;
}
