# health Specification

## Purpose
Say whether the control plane is working, and when it is not, name the fact that fails. Liveness is a constant that
proves the process is up; readiness reads the dependencies and answers with one failure per stale or never-read fact,
each with its age and threshold.

## Requirements

### Requirement: Readiness names the fact that fails
`GET /api/ready` SHALL answer 200 only when the store answers a read, every project's mirror heartbeat is younger
than the readiness threshold, the rig queue's last read succeeded, and the last tick completed; otherwise it SHALL
answer 503 with a body naming each failing fact and the number that failed it.

#### Scenario: Healthy control plane
- **WHEN** the store answers, both projects read within the threshold, the queue read is fresh and a tick completed
- **THEN** readiness answers 200 with an empty list of failures

#### Scenario: A stalled mirror
- **WHEN** the last successful forge read is older than the readiness threshold
- **THEN** readiness answers 503 naming `mirror.<project>`, the heartbeat time and its age

#### Scenario: A probe cannot restart a healthy service
- **WHEN** a slow forge read is in flight but the previous read is inside the threshold
- **THEN** readiness still answers 200

### Requirement: Liveness stays a constant
`GET /api/health` SHALL answer 200 with a fixed body and SHALL NOT read the store, the rig or the forge.

#### Scenario: Dependencies are down
- **WHEN** the store is closed and the rig is unreachable
- **THEN** `/api/health` still answers 200, because the process is alive and a restart is not the repair
