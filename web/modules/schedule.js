export function renderSchedule(container, schedule = {}, onChange = () => {}) {
  container.replaceChildren();
  const fields = [
    ['enabled', 'Enabled', Boolean(schedule.enabled)],
    ['timezone', 'Timezone', schedule.timezone ?? ''],
    ['productivity_minutes', 'Productivity minutes', schedule.productivity_minutes ?? 0],
    ['probe_timeout_seconds', 'Probe timeout seconds', schedule.probe_timeout_seconds ?? 0],
  ];
  for (const [name, label, value] of fields) {
    const wrapper = document.createElement('div');
    wrapper.className = 'field';
    const input = document.createElement(name === 'enabled' ? 'input' : 'input');
    input.id = `schedule-${name}`;
    input.name = name;
    input.type = name === 'enabled' ? 'checkbox' : 'text';
    if (name === 'enabled') input.checked = value;
    else input.value = value;
    input.addEventListener('change', () => onChange(name, name === 'enabled' ? input.checked : input.value));
    const labelNode = document.createElement('label');
    labelNode.htmlFor = input.id;
    labelNode.textContent = label;
    wrapper.append(labelNode, input);
    container.append(wrapper);
  }
}

export function readScheduleDraft(container, original = {}) {
  const draft = { ...original };
  for (const input of container.querySelectorAll('[name]')) {
    if (input.type === 'checkbox') {
      draft[input.name] = input.checked;
    } else if (['productivity_minutes', 'probe_timeout_seconds'].includes(input.name)) {
      draft[input.name] = Number(input.value);
    } else {
      draft[input.name] = input.value;
    }
  }
  return draft;
}
