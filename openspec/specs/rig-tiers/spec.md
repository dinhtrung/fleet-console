# rig-tiers Specification

## Purpose
A project declares its rigs in tiers with a spill trigger, and dispatch walks them in order, naming the tier and trigger when it cannot.

## Requirements

### Requirement: A project declares its rigs in tiers with a spill trigger

Each `rigs` entry SHALL be a name with an optional `tier` and `spill` trigger. A bare string SHALL mean tier 0 with
`spill: on-full`. `spill` SHALL be one of `never`, `on-full` or `on-unavailable`; any other value is a config error
naming it.

#### Scenario: The existing flat list

- **WHEN** a project declares `rigs: [primary, secondary]`
- **THEN** both resolve to tier 0 with `spill: on-full`, and the project's dispatch order is unchanged

#### Scenario: An unknown trigger

- **WHEN** an entry sets `spill: when-busy`
- **THEN** loading the config fails and names the entry and the value

#### Scenario: One rig named twice

- **WHEN** a project lists the same rig in two entries
- **THEN** loading the config fails, because its seats would be counted twice

### Requirement: Seats in an earlier tier are used before a later one

The plan SHALL fill free seats tier by tier in the declared order, and SHALL consider a later tier only when every
earlier tier is exhausted and that entry's spill trigger holds. A seat on a rig the project does not declare SHALL
be ignored.

#### Scenario: The primary has a free seat

- **WHEN** tier 0 has one free seat and one item is ready
- **THEN** the item is dispatched to that seat and the later tier is not consulted

#### Scenario: A seat on a foreign rig

- **WHEN** a running seat's rig is not in the project's list
- **THEN** the plan never dispatches to it

### Requirement: The spill trigger decides when a later tier is used

`on-full` SHALL allow the later tier when no free seat remains in the earlier tiers, the concurrency cap included.
`on-unavailable` SHALL allow it only when an earlier-tier rig has no running seat or its read failed. `never` SHALL
keep the later tier unused while any earlier tier exists.

#### Scenario: Overflow on a full tier

- **WHEN** every tier-0 seat is busy and the tier-1 entry says `spill: on-full`
- **THEN** the ready item is dispatched to a free tier-1 seat

#### Scenario: A standby rig stays cold

- **WHEN** every tier-0 seat is busy and the tier-1 entry says `spill: on-unavailable`
- **THEN** the item is parked with a named skip and no tier-1 seat is used

#### Scenario: The primary is down

- **WHEN** the tier-0 rig has zero running seats and the tier-1 entry says `spill: on-unavailable`
- **THEN** the item is dispatched to tier 1

### Requirement: The refusal names the tier and the trigger

When a ready item is not dispatched because no tier qualifies, the skip SHALL name the tier that was refused and the
fact that refused it, in the same line the journal already prints.

#### Scenario: Reading a spill refusal

- **WHEN** a ready item parks because the only later tier is `on-unavailable` and its rig is running
- **THEN** the skip line names the tier and the trigger, so a busy fleet is distinguishable from a misconfigured one
