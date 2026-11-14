## ADDED Requirements

### Requirement: Isolation is one project per rig

Each rig SHALL serve exactly one project, and a project MAY name several rigs in tiers. Two projects naming the same rig
SHALL be a config error naming both keys, and neither SHALL dispatch until it is resolved. Every seat of a rig SHALL be
launched with a cwd inside its project's checkout or a scratch root that project owns.

#### Scenario: Two projects on one rig

- **WHEN** two project rows name the same rig
- **THEN** the board reports the conflict and neither dispatches until it is resolved

#### Scenario: A project that grows a second rig

- **WHEN** a project adds a second rig launched from its own checkout
- **THEN** both rigs dispatch for that project, each with its own reviewer, and no item appears in the other project
