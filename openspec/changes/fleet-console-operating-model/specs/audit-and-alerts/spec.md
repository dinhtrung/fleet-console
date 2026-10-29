## ADDED Requirements

### Requirement: A tier change is one event with a reason

Every tier change SHALL write exactly one event naming the project, the tier left, the tier entered, and the reason
(`full`, `quota`, `down`, `stalled`). It SHALL escalate once through the alert channel. Ticks that stay in the same
tier SHALL NOT repeat the event or the alert.

#### Scenario: A quota failover

- **WHEN** tier 1 goes unhealthy and tier 2 takes over
- **THEN** one event reads `tier 1 → 2, quota`, one alert is sent, and later ticks are silent

#### Scenario: A second switch in the same day

- **WHEN** tier 2 later stalls and tier 1 is brought back by hand
- **THEN** a second event and a second alert are written, each naming its own reason
