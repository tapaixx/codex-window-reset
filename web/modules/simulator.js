export function renderSimulation(container, result = {}) {
  container.replaceChildren();
  const summary = document.createElement('p');
  summary.textContent = `Coverage ${result.scheduled?.available_coverage_minutes ?? 0} minutes; idle ${result.scheduled?.idle_window_minutes ?? 0} minutes.`;
  container.append(summary);
}
