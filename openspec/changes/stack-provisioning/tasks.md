# Tasks — stack provisioning and packaging

A box is ticked only with the command that proved it. The script is tested on a throwaway container or a fresh VM
before it is pointed at a box that runs a fleet.

## 1. Lock and preflight

- [ ] 1.1 `stack.lock` with one section per class (runtime, rig, harness, forge, tooling, app) and the app's sha256; a `--lock` override path. Verify: `./install.sh --dry-run` prints every pin and exits 0 without writing.
- [ ] 1.2 Preflight: OS/arch, sudo, disk ≥ 20 GB, ports 8787 free, existing versions detected and reported. Verify: dry-run on the current box lists the installed versions beside the pins; a wrong arch aborts before any write.
- [ ] 1.3 Profile selection (`fleet`, `app`) decides the step list; `app` skips rig and harness. Verify: `--dry-run --profile app` prints only the binary, unit and verify steps.

## 2. Install steps

- [ ] 2.1 Packages: `git tmux jq curl unzip sqlite3` via apt, Node 24 and Go 1.25 from their own channels, all checksum-verified where the vendor publishes one. Verify: each step prints the installed version and the step's exit code.
- [ ] 2.2 OpenRig CLI at the locked version into `~/.local`, then `rig setup --dry-run` and `rig setup`; record that Codex is expected to fail. Verify: `rig --version` equals the lock; `rig doctor` shows only the two expected findings, quoted in the log.
- [ ] 2.3 Tooling: `serena` via `uv tool install`, `codegraph` and `rtk` from their release assets, `gopls` via `go install`. Verify: each tool answers `--version` with the seat's `PATH`, not the login shell's.
- [ ] 2.4 The app: binary from the release tarball (sha256 checked), `deploy/fleet-console.service`, an env file at mode 600, and the rig template directory. Verify: `systemctl --user is-active fleet-console` and `curl -fsS 127.0.0.1:8787/api/health`.

## 3. Human stops

- [ ] 3.1 Four stops: harness login, `gh auth login`, `glab auth login`, seat provider key. Each prints one copy-pasteable command, exits `3`, and writes nothing secret to the log. Verify: a run on a fresh box stops at the first one with exit 3, and `grep -i 'sk-\|ghp_\|api_key' latest.log` returns nothing.

## 4. Verify

- [ ] 4.1 `--verify` checks: pinned versions, `rig doctor` with its two expected findings, daemon running, a real `gh`/`glab` read, `fleet-console --compat` (rig version + floor + two parsed shapes), `/api/health` 200, units `active`+`enabled`, linger on. Verify: on a complete box every check prints `ok` and the script exits 0; deleting one tool makes exactly its check fail.
- [ ] 4.2 A drift test asserting `stack.lock`'s OpenRig pin satisfies the app's `runtime-compat` floor. Verify: `go test ./internal/rig -run TestLockSatisfiesFloor` fails when the lock is edited below the floor.

## 5. Idempotency and safety

- [ ] 5.1 Re-run safety: a second `--apply` on an installed box changes nothing, and every edited config keeps a `.bak-<date>`. Verify: run twice, diff the tree between runs, and show the marker block is present once, not twice.
- [ ] 5.2 No clobber: `~/.claude.json`, `~/.tmux.conf`, `~/.gitconfig`, `~/.openrig/specs/rigs/*` are read and merged, never replaced. Verify: a box with a pre-existing spec still has it after install, byte-identical.

## 6. Packaging

- [ ] 6.1 `Makefile` targets `web` (`npm ci && npm run build`), `build` (`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`), `test`, `release` (tar.gz + sha256). Verify: `make build` produces one file; `CGO_ENABLED=0 go build ./...` is clean; the file embeds the board (serves `/` with no `dist/` beside it).
- [ ] 6.2 `--version --json` prints version, commit, build time and the rig floor; `--compat` prints the observed rig version, the floor and the two parsed shapes. Verify: both commands' output recorded in the release notes.
- [ ] 6.3 Boot dependency check: `rig`, `gh`/`glab`, `git`, `tmux` on `PATH`; a missing one is named at startup and the act loop stays off. Verify: run with an empty `PATH` and read the message.
- [ ] 6.4 `Dockerfile` (multi-stage) with the binary, `gh`, `glab`, `rig`, `git`, `tmux`, and a header comment naming the mounts required to reach a host rig. Verify: `docker build` succeeds and `docker run --rm <img> --version` prints the same version as the binary.
- [ ] 6.5 Upgrade path: `./install.sh --upgrade` stops the unit, backs up the DB, swaps the binary, migrates, starts, verifies; a failed verify restores the previous binary. Verify: a forced failure leaves the old binary running and the DB backup named in the log.

## 7. Documentation

- [ ] 7.1 One page `docs/install.md`: profiles, the four stops, what verify proves, the Docker exception, and the upgrade path. Verify: a fresh box is installed by following only that page, and the transcript is attached to the task.
