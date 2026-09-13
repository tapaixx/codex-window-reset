# UI Review Fixes Implementation Plan

> Execute inline with executing-plans and test-driven-development. User requested direct execution without subagents. This repair-only pass does not include commits or releases.

**Goal:** Resolve the UI review findings while preserving host API contracts, schedule/account separation, and existing uncommitted work.

**Architecture:** Keep the current static HTML/CSS and ES modules. Refresh operations return explicit outcomes; draft configuration remains separate from remote refreshes. All timeline elements share one time domain and axis gutter. No backend or credential persistence changes.

**Tech Stack:** Vanilla JavaScript, CSS, Go embedded panel, Node test runner and Chromium/CDP.

**Spec:** User-approved UI review immediately preceding this plan; confirmed focused workday axis, explicit A/B descriptions, zero-gain messaging, and refresh-selected versus refresh-all labels.

## Constraints

- Work in the existing feature worktree; preserve all previous edits.
- No real quota consumption during verification. Fixture host API only.
- Empty scheduled account collection stays valid and does not trigger automatic work.
- Keep published version unchanged; no release authorization this turn.

## Task 1: Regression coverage

Files: `web/tests/browser.test.mjs`, `web/tests/browser-harness.js`, `web/tests/timeline.test.mjs`.

- [x] Add independent browser scenarios for refresh failures/partial success/no enabled accounts/re-enabled credentials; expose fixture-only response controls.
- [x] Add browser assertions for draft preservation, field error focus, valid zero thresholds, mobile checkbox visibility, modal names, reduced motion, dark surfaces, and axis alignment.
- [x] Add pure time-domain tests: normal workday 05:00–20:00 with preheat at 06:00, near-midnight clipping, empty-day fallback.
- [x] Run `BROWSER_BIN=/root/.cache/ms-playwright/chromium-1234/chrome-linux/chrome node --test --test-isolation=none web/tests/*.test.mjs`; observe failures on the reviewed defects.

## Task 2: Refresh and configuration feedback

Files: `web/modules/main.js`, `web/panel.html`.

- [x] Return `{succeeded, failed}` from quota refresh and calculate skipped counts at the caller; use settled results and preserve successful snapshots. Mark failed snapshots stale; show outcome counts, never unconditional success.
- [x] Rediscover auth files before refresh-all, filter disabled accounts, retain valid manual selection and unchanged scheduled membership. Disable conflicting refresh buttons while active.
- [x] Track a saved draft signature. Header refresh calls `loadPanel()` with automatic dirty-draft protection; do not apply remote schedule to a dirty form. Warn on unload; show unsaved state. Preserve edits made during save.
- [x] Validate native ranges and cross-field time/preheat constraints before submit; associate inline messages with `aria-describedby`, focus first invalid control and open advanced section if needed. Preserve 0 values instead of replacing them with defaults. Show saving state.
- [x] Rerun browser interaction scenarios.

## Task 3: Time axis and visual accessibility

Files: `web/modules/timeline.js`, `web/simulator.css`, `web/styles.css`, `web/panel.html`.

- [x] Derive a clamped, hour-rounded domain from work/coverage/preheat times with one-hour padding. Convert every tick, band, boundary and preheat point through that domain; boundary overlay shares the lanes' 86px gutter.
- [x] Explain A/B strategies and render neutral zero-gain copy without inventing benefits.
- [x] Restore mobile selection controls; add visible switch focus, dark tokens across the whole page/dialogs, anchor offset, reduced-motion navigation, labels and dialog titles.
- [x] Run all tests plus syntax checks. Capture and inspect fresh desktop/mobile/light/dark screenshots.

## Task 4: Verification and handoff

- [x] Verify the embedded panel with the existing Go fixture export path if local tooling permits; otherwise explicitly report that limitation.
- [x] Review the diff and record exact passing checks and remaining limitations here. Do not commit or publish.

## Verification results (2026-09-12)

- Baseline: 51/51 Node/browser tests passed before implementation. Plain sandbox test execution was restricted; Chromium and local fixture servers ran with approved escalation.
- Regression proof: all 11 new cases failed before repair; corrected fixture requests to use the real `auth_index` field and re-confirmed refresh failure cases. Added checks for odd-hour final ticks, narrow-screen action width and dark label contrast also failed before their fixes.
- Final source: 62/62 tests passed, 0 skipped, with Chromium. Viewports: 375, 768, 1024 and 1440 pixels; light, dark and reduced-motion scenarios.
- Final Go verification: all 11 packages with tests passed in the existing offline `golang:1.24-bookworm` container. Source mounted read-only. Actual embedded panel and Go simulator output exported to `/tmp/cwr-ui-embedded.x3qdIi`.
- Final embedded response: 62/62 tests passed, 0 skipped, using exported HTML and Go simulation fixture; secondary JS/CSS asset requests remained empty. Embedded badge remains `v0.0.10`; no version bump or release in this pass.
- `npm run check` and `git diff --check` passed.
- Fresh screenshots inspected: `/tmp/cwr-ui-review-final/simulator-1440.png`, `simulator-dark-1440.png`, `simulator-375.png` and `audit-accessibility-375.png` in the same directory. Dark markers meet the tested 4.5:1 text contrast threshold; mobile actions use a two-column grid.
- Browser tests use a local fixture host only. No actual user-host deployment, live quota refresh, real probe, or reset consumption was tested.
- Changes reside in `/share/codeSpace/codex-window-reset/.worktrees/codex-window-reset`. The older temporary Git clone `/tmp/cwr-git` was not synchronized in this turn; do not release from it without copying/reviewing the current worktree changes.

## Authorized release continuation

The user subsequently requested submission and release. The corrected files were synchronized and compared against `/tmp/cwr-git`, whose HEAD matched remote main at `4f9b880`. Release version is `v0.0.11`; README and registry metadata were updated. Fresh source and versioned Go-embedded browser suites both passed 62/62 tests without skips. Go vet, race tests, all Go package tests, native arm64 shared-library build, actionlint, shell syntax and diff checks also passed before commit/tag creation. Production host consumption remains untested; GitHub Actions will verify both supported architectures before publishing assets.
