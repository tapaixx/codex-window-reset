// Same hand-checked Monday, 09:00–12:00 / 13:30–18:00 example as the Go export.
const instant = (clock) => `2026-09-14T${clock}:00+08:00`;
const segment = (kind, start, end) => ({ kind, start: instant(start), end: instant(end) });
export const simulationFixture = {
  work_minutes: 450,
  baseline: { available_coverage_minutes: 120, idle_window_minutes: 150, timeline_segments: [
    segment('available', '09:00', '10:00'), segment('limited', '10:00', '12:00'),
    segment('break', '12:00', '13:30'), segment('limited', '13:30', '14:00'), segment('available', '14:00', '15:00'), segment('limited', '15:00', '18:00'),
  ] },
  scheduled: { available_coverage_minutes: 180, idle_window_minutes: 450, timeline_segments: [
    segment('available', '09:00', '10:00'), segment('limited', '10:00', '11:30'), segment('available', '11:30', '12:00'),
    segment('break', '12:00', '13:30'), segment('available', '13:30', '14:00'),
    segment('limited', '14:00', '16:30'), segment('available', '16:30', '17:30'), segment('limited', '17:30', '18:00'),
  ] },
  net_gain_minutes: 60,
  preheat_windows: [
    { id: 'example/p0', period_index: 0, window_start: instant('06:00'), window_end: instant('07:00'), planned_at: instant('06:30') },
    { id: 'example/p1', period_index: 1, window_start: instant('11:00'), window_end: instant('12:00'), planned_at: instant('11:30') },
    { id: 'example/p2', period_index: 2, window_start: instant('16:00'), window_end: instant('17:00'), planned_at: instant('16:30') },
  ],
  timeline_segments: [
    segment('preheat', '06:30', '06:30'), segment('work', '09:00', '12:00'),
    segment('preheat', '11:30', '11:30'), segment('work', '13:30', '18:00'),
    segment('preheat', '16:30', '16:30'),
  ],
  assumptions: { productivity_minutes: 60 },
};
