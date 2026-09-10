# Codex Window Management

This context describes how Codex accounts, usage windows, and deliberate reset operations are discussed by Codex Window Reset.

## Language

**Codex Account**:
A CLIProxyAPI-managed Codex credential whose health and upstream usage limits can be observed independently.
_Avoid_: User, login

**Usage Window**:
An upstream time-bounded allowance during which a Codex Account can consume quota before the allowance naturally renews.
_Avoid_: Session, billing cycle

**Preheat Request**:
A Probe Request made by the scheduler with the intent of beginning a Usage Window before expected interactive work. It consumes ordinary quota and does not consume a Reset Credit.
_Avoid_: Reset, forced reset, keepalive

**Quota Reset**:
An explicit, user-authorized operation that consumes a Reset Credit to restore upstream quota. It is never an automatic scheduling action.
_Avoid_: Preheat, refresh, restart

**Reset Credit**:
A limited upstream entitlement consumed by one Quota Reset.
_Avoid_: Quota, token balance

**Health Probe**:
A manually authorized Probe Request whose intent is to determine whether a Codex Account can currently complete a request successfully. It may begin a Short Window and consume ordinary quota.
_Avoid_: Preheat, ping

**Probe Request**:
A minimal real Codex model-generation request used by either a Health Probe or a Preheat Request.
_Avoid_: Read-only check, quota refresh

**Request Outcome**:
The result of executing a Probe Request against Codex, independent of whether a Usage Window change can be observed.
_Avoid_: Window status, preheat result

**Window Outcome**:
The observed Usage Window effect associated with a Probe Request, classified independently from its Request Outcome.
_Avoid_: Request status, health status

**Operator**:
The single trusted administrator of one CLIProxyAPI instance who may inspect all discovered Codex Accounts and change window-management settings.
_Avoid_: User, viewer, member

**Scheduled Account**:
A Codex Account that the Operator has explicitly selected to receive automatic Preheat Requests.
_Avoid_: Enabled account, included account

**Action Selection**:
A temporary set of Codex Accounts chosen for the next manual bulk action, with no effect on Scheduled Account membership.
_Avoid_: Scheduled accounts, managed accounts

**Preheat Schedule**:
The Operator-approved rules that determine when Scheduled Accounts may receive Preheat Requests.
_Avoid_: AI schedule, automatic recommendation

**Schedule Recommendation**:
A proposed change to the Preheat Schedule derived from observed usage history that has no effect until the Operator accepts it.
_Avoid_: Automatic optimization, self-adjustment

**Critical Work Period**:
A configured period in which the Operator expects a Scheduled Account's Usage Window to be available for interactive work.
_Avoid_: Office hours, uptime

**Available Coverage**:
The overlap between a usable Usage Window and a Critical Work Period.
_Avoid_: Window duration, quota duration

**Idle Window**:
The portion of a Usage Window that falls outside all Critical Work Periods and therefore represents likely wasted availability.
_Avoid_: Unused quota

**Sufficient Window**:
An existing Usage Window whose remaining time and quota both meet the configured minimums, so a planned Preheat Request is unnecessary.
_Avoid_: Healthy account, full quota

**Compensation Attempt**:
The single delayed retry allowed after an eligible automatic Preheat Request fails.
_Avoid_: Retry loop, quota reset

**Missed Occurrence**:
A planned account execution whose Preheat Window elapsed before the request ran; it is recorded and never executed later as catch-up work.
_Avoid_: Failed request, compensation attempt

**Paused Account**:
A Scheduled Account temporarily prevented from receiving automatic Preheat Requests because it is unavailable or unhealthy, without removing the Operator's selection. An unavailable account may still receive an explicitly authorized Health Probe; a disabled account may not.
_Avoid_: Excluded account, removed account

**Reset Audit**:
A durable record that an Operator requested a Quota Reset and whether the upstream operation succeeded.
_Avoid_: Detection history, application log

**Work Calendar**:
The effective weekdays, timezone, and ordered Critical Work Periods that constrain a Preheat Schedule.
_Avoid_: Office calendar, lunch configuration

**Remaining Quota Floor**:
The minimum remaining short-window quota required for an existing Usage Window to be considered sufficient.
_Avoid_: Health threshold, used percentage

**Remaining Window Floor**:
The minimum remaining short-window duration required for an existing Usage Window to be considered sufficient.
_Avoid_: Preheat duration, work duration

**Short Window**:
The upstream Usage Window whose comparatively brief renewal cycle determines whether and when to preheat.
_Avoid_: Five-hour window

**Long Window**:
An upstream Usage Window with a longer renewal cycle that constrains whether further automatic quota consumption is safe.
_Avoid_: Weekly quota, monthly quota

**Quota Guardrail**:
A minimum Long Window allowance below which automatic Preheat Requests pause regardless of Short Window availability.
_Avoid_: Health threshold, preheat threshold

**Guardrail Hold**:
A persistent block on automatic Preheat Requests created by the last successful snapshot showing a Long Window below its Quota Guardrail, cleared only by a later successful snapshot proving recovery.
_Avoid_: Stale snapshot, account pause

**Usage Snapshot**:
A point-in-time observation of a Codex Account's upstream quota windows, captured manually or around an operation that can observe or affect quota.
_Avoid_: Usage history, live telemetry

**Recommendation Confidence**:
The stated strength of evidence behind a Schedule Recommendation derived from Usage Snapshots.
_Avoid_: Accuracy, guarantee

**Operational History**:
The recent bounded record of Health Probes, Preheat Requests, skips, compensation attempts, and their outcomes.
_Avoid_: Reset audit, server log

**Preheat Window**:
A bounded interval before a Critical Work Period during which each eligible Scheduled Account may receive at most one Preheat Request.
_Avoid_: Preheat duration, repeated warmup

**Productivity Estimate**:
An Operator-configured estimate of productive minutes available from one Short Window, used only by the strategy simulator and never treated as upstream quota data.
_Avoid_: Quota minutes, actual usage

**Blackout Period**:
A weekly recurring time interval in which automatic Preheat Requests are forbidden.
_Avoid_: Skip time, disabled schedule

**Stale Snapshot**:
A Usage Snapshot too old to support a current quota decision without refresh.
_Avoid_: Unavailable quota, zero quota
