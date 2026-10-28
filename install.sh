#!/usr/bin/env bash
# fleet-console stack installer.
#
# Dry-run is the default: without --apply nothing is written. Every install
# goes through a vendor channel and is checked against stack.lock. Secrets are
# never printed; steps that need one stop with exit 3 and a command to run.
#
# Usage:
#   ./install.sh [--dry-run] [--apply] [--profile fleet|app]
#   ./install.sh --verify
#   ./install.sh --upgrade [--app-tarball PATH | --app-binary PATH]
set -Eeuo pipefail

SELF_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
LOCK_FILE="$SELF_DIR/stack.lock"
PROFILE=fleet
MODE=dry-run
ACTION=install
APP_BINARY=
APP_TARBALL=
MARKER="# >>> fleet-console stack >>>"
MARKER_END="# <<< fleet-console stack <<<"
LOG_DIR="$HOME/.local/state/fleet-stack"
LOG_FILE=
FAILED=0
STEP_NO=0

declare -A LOCK=()

# ---------------------------------------------------------------- arguments

usage() {
  sed -n '2,12p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit 0
}

while (($#)); do
  case $1 in
    --dry-run) MODE=dry-run ;;
    --apply) MODE=apply ;;
    --profile) PROFILE=${2:?--profile needs fleet or app}; shift ;;
    --profile=*) PROFILE=${1#--profile=} ;;
    --verify) ACTION=verify; MODE=apply ;;
    --upgrade) ACTION=upgrade; MODE=apply ;;
    --lock) LOCK_FILE=${2:?--lock needs a path}; shift ;;
    --lock=*) LOCK_FILE=${1#--lock=} ;;
    --app-binary) APP_BINARY=${2:?--app-binary needs a path}; shift ;;
    --app-binary=*) APP_BINARY=${1#--app-binary=} ;;
    --app-tarball) APP_TARBALL=${2:?--app-tarball needs a path}; shift ;;
    --app-tarball=*) APP_TARBALL=${1#--app-tarball=} ;;
    -h|--help) usage ;;
    *) echo "install.sh: unknown argument $1" >&2; exit 2 ;;
  esac
  shift
done

case $PROFILE in
  fleet|app) ;;
  *) echo "install.sh: profile must be fleet or app, got '$PROFILE'" >&2; exit 2 ;;
esac

# ------------------------------------------------------------------ helpers

trim() { local s=${1#"${1%%[![:space:]]*}"}; printf '%s' "${s%"${s##*[![:space:]]}"}"; }

emit() { printf '%s\n' "$*"; }

die() { emit "error: $*" >&2; exit 1; }

log_start() {
  mkdir -p "$LOG_DIR"
  LOG_FILE="$LOG_DIR/install-$(date -u +%Y%m%dT%H%M%SZ).log"
  : >"$LOG_FILE"
  exec > >(tee -a "$LOG_FILE") 2>&1
  ln -sfn "$(basename "$LOG_FILE")" "$LOG_DIR/latest.log"
  emit "log: $LOG_FILE"
}

load_lock() {
  [[ -f $LOCK_FILE ]] || die "lock file not found: $LOCK_FILE"
  local line key value
  while IFS= read -r line || [[ -n $line ]]; do
    line=${line%%#*}
    [[ $line == *=* ]] || continue
    key=$(trim "${line%%=*}")
    value=$(trim "${line#*=}")
    [[ -n $key && -n $value ]] && LOCK["$key"]=$value
  done <"$LOCK_FILE"
}

pin() {
  [[ -n ${LOCK[$1]:-} ]] || die "stack.lock has no '$1' pin"
  printf '%s' "${LOCK[$1]}"
}

cmd_for() {
  case $1 in
    openrig) echo rig ;;
    fleet_console) echo fleet-console ;;
    *) echo "$1" ;;
  esac
}

version_arg_for() {
  case $1 in
    go) echo version ;;
    gopls) echo version ;;
    tmux) echo -V ;;
    *) echo --version ;;
  esac
}

installed_version() {
  local key=$1 cmd arg out
  cmd=$(cmd_for "$key")
  arg=$(version_arg_for "$key")
  command -v "$cmd" >/dev/null 2>&1 || return 1
  out=$("$cmd" "$arg" 2>&1 | head -1)
  printf '%s' "$out" | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1
}

version_matches() {
  local want=$1 got=$2
  [[ -n $got ]] || return 1
  if [[ $want == *.x ]]; then
    local prefix=${want%.x}
    [[ $got == "$prefix" || $got == "$prefix".* ]]
  else
    [[ $got == "$want" ]]
  fi
}

version_state() {
  local key=$1 want got
  want=$(pin "$key")
  if ! got=$(installed_version "$key"); then
    echo "missing"
  elif version_matches "$want" "$got"; then
    echo "ok $got"
  else
    echo "differs $got"
  fi
}

require_root_or_sudo() {
  if [[ $EUID -eq 0 ]]; then return 0; fi
  sudo -n true >/dev/null 2>&1 || die "passwordless sudo is required for apt packages; run 'sudo -v' and retry"
}

apt_install() {
  [[ $# -gt 0 ]] || return 0
  require_root_or_sudo
  if [[ $EUID -eq 0 ]]; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y "$@"
  else
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y "$@"
  fi
}

sha256_of() { sha256sum "$1" | awk '{print $1}'; }

fetch() { curl -fsSL --retry 2 --connect-timeout 10 -o "$2" "$1"; }

# merge_block writes content between the marker lines, keeping the rest of the
# file. Identical content is a no-op; a change keeps a dated backup.
merge_block() {
  local file=$1 content=$2 existing tmp
  mkdir -p "$(dirname "$file")"
  [[ -f $file ]] || : >"$file"
  existing=$(awk -v s="$MARKER" -v e="$MARKER_END" '
    index($0, s) {inside=1}
    inside {print}
    index($0, e) {inside=0}' "$file")
  if [[ $existing == "$content" ]]; then
    return 1
  fi
  if [[ -s $file ]]; then
    cp -p "$file" "$file.bak-$(date -u +%Y%m%dT%H%M%SZ)"
  fi
  tmp=$(mktemp)
  awk -v s="$MARKER" -v e="$MARKER_END" '
    index($0, s) {inside=1; next}
    index($0, e) {inside=0; next}
    !inside {print}' "$file" >"$tmp"
  printf '%s\n' "$content" >>"$tmp"
  mv "$tmp" "$file"
  return 0
}

path_block() {
  cat <<'EOF'
# >>> fleet-console stack >>>
case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) PATH="$HOME/.local/bin:$PATH" ;; esac
export PATH
# <<< fleet-console stack <<<
EOF
}

human_stop() {
  emit "needs-human: $1"
  emit "then run: ./install.sh --verify"
  exit 3
}

# ---------------------------------------------------------------- preflight

preflight() {
  local os arch avail_kb listeners
  os=$(. /etc/os-release 2>/dev/null && echo "${ID:-unknown}")
  case $os in
    ubuntu|debian) ;;
    *) die "unsupported OS '$os': this installer supports ubuntu and debian" ;;
  esac
  arch=$(uname -m)
  case $arch in
    x86_64|aarch64) ;;
    *) die "unsupported architecture '$arch'" ;;
  esac
  avail_kb=$(df -Pk "$HOME" | awk 'NR==2 {print $4}')
  if [[ -n $avail_kb ]] && ((avail_kb < 20 * 1024 * 1024)); then
    die "less than 20 GB free under $HOME ($((avail_kb / 1024 / 1024)) GB)"
  fi
  if curl -fsS --max-time 2 http://127.0.0.1:8787/api/health >/dev/null 2>&1; then
    :
  elif command -v ss >/dev/null 2>&1 && ss -ltn 2>/dev/null | grep -qE '[:.]8787[[:space:]]'; then
    die "port 8787 is in use by another process"
  fi
  if [[ $PROFILE == fleet && $MODE == apply && $EUID -ne 0 ]]; then
    sudo -n true >/dev/null 2>&1 || die "fleet --apply needs sudo for apt; run 'sudo -v' and retry"
  fi
  emit "preflight: os=$os arch=$arch free=$((avail_kb / 1024 / 1024))GB port8787=free-or-ours"
}

# -------------------------------------------------------------------- steps

PROFILE_KEYS_FLEET="node go openrig omp gh glab git tmux jq sqlite3 uv gopls serena codegraph rtk fleet_console"
PROFILE_KEYS_APP="openrig git tmux gh glab fleet_console"

step_keys() {
  case $1 in
    fleet) echo "$PROFILE_KEYS_FLEET" ;;
    app) echo "$PROFILE_KEYS_APP" ;;
  esac
}

step_list() {
  case $PROFILE in
    fleet) echo "path packages openrig rigsetup tooling harness_stop gh_stop glab_stop seat_stop app units verify" ;;
    app) echo "app units verify" ;;
  esac
}

step_desc() {
  case $1 in
    path) echo "merge the PATH marker block into ~/.profile" ;;
    packages) echo "install apt packages, Node $(pin node), Go $(pin go)" ;;
    openrig) echo "install @openrig/cli $(pin openrig) into ~/.local" ;;
    rigsetup) echo "run 'rig setup' (Codex is expected to fail)" ;;
    tooling) echo "install serena/gopls/codegraph/rtk at their pins" ;;
    harness_stop) echo "human: sign in to the coding harness" ;;
    gh_stop) echo "human: gh auth login" ;;
    glab_stop) echo "human: glab auth login" ;;
    seat_stop) echo "human: provider key for seat credentials" ;;
    app) echo "install fleet-console $(pin fleet_console), env file (0600) and unit" ;;
    units) echo "enable and start user units; enable linger" ;;
    verify) echo "run ./install.sh --verify (the only success signal)" ;;
    *) echo "$1" ;;
  esac
}

harness_ready() {
  command -v omp >/dev/null 2>&1 || return 1
  omp auth export >/dev/null 2>&1
}

seat_key_present() {
  local f
  [[ -n ${OMP_PROVIDER_KEY:-}${ANTHROPIC_API_KEY:-}${OPENAI_API_KEY:-} ]] && return 0
  for f in "$HOME"/.openrig/state/omp/*/agent/config.yml; do
    [[ -f $f ]] && return 0
  done
  return 1
}

binary_satisfied() {
  local target="$HOME/.local/bin/fleet-console"
  [[ -x $target ]] || return 1
  local got want_sha
  got=$(version_of_binary "$target") || return 1
  [[ $got == "$(pin fleet_console)" ]] || return 1
  want_sha=${LOCK[fleet_console_binary_sha256]:-}
  [[ -z $want_sha || $want_sha == pending ]] || [[ $(sha256_of "$target") == "$want_sha" ]]
}

version_of_binary() {
  "$1" --version --json 2>/dev/null | sed -n 's/.*"version": "\([^"]*\)".*/\1/p' | head -1
}

env_file_satisfied() {
  local f="$HOME/.config/fleet-console/fleet-console.env"
  [[ -f $f ]] || return 1
  [[ $(stat -c '%a' "$f") == 600 ]] || return 1
  grep -q '^FLEET_CONSOLE_TOKEN=.\+' "$f"
}

unit_file_satisfied() {
  local name=$1
  local src="$SELF_DIR/deploy/$name.service"
  local dst="$HOME/.config/systemd/user/$name.service"
  [[ -f $dst ]] || return 1
  cmp -s "$src" "$dst"
}

unit_state() {
  local name=$1
  systemctl --user is-active "$name" 2>/dev/null | grep -qx active &&
    systemctl --user is-enabled "$name" 2>/dev/null | grep -qx enabled
}

package_installed() {
  dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q 'install ok installed'
}

step_satisfied() {
  local name=$1
  case $name in
    preflight) return 0 ;;
    path) [[ $(awk -v s="$MARKER" 'index($0,s){n++} END{print n+0}' "$HOME/.profile" 2>/dev/null) == 1 ]] ;;
    packages)
      local p
      for p in git tmux jq curl unzip sqlite3; do
        command -v "$p" >/dev/null 2>&1 || return 1
      done
      package_installed ca-certificates || return 1
      version_matches "$(pin node)" "$(installed_version node || true)" &&
        version_matches "$(pin go)" "$(installed_version go || true)"
      ;;
    openrig) version_matches "$(pin openrig)" "$(installed_version openrig || true)" ;;
    rigsetup) [[ -d $HOME/.openrig ]] ;;
    tooling)
      version_matches "$(pin serena)" "$(installed_version serena || true)" &&
        version_matches "$(pin gopls)" "$(installed_version gopls || true)" &&
        version_matches "$(pin codegraph)" "$(installed_version codegraph || true)" &&
        version_matches "$(pin rtk)" "$(installed_version rtk || true)"
      ;;
    harness_stop) harness_ready ;;
    gh_stop) gh auth status >/dev/null 2>&1 ;;
    glab_stop) glab auth status >/dev/null 2>&1 ;;
    seat_stop) seat_key_present ;;
    app) binary_satisfied && env_file_satisfied && unit_file_satisfied fleet-console ;;
    units)
      unit_state fleet-console || return 1
      if [[ $PROFILE == fleet ]]; then unit_state rig-daemon || return 1; fi
      loginctl show-user "$(id -u)" -p Linger 2>/dev/null | grep -qx 'Linger=yes'
      ;;
    *) return 1 ;;
  esac
}

install_node() {
  local want base file sum tmp
  want=$(pin node)
  base="https://nodejs.org/dist/latest-v${want%.x}.x"
  tmp=$(mktemp -d)
  fetch "$base/SHASUMS256.txt" "$tmp/SHASUMS256.txt"
  case $(uname -m) in
    x86_64) file=$(grep -oE 'node-v[0-9.]+-linux-x64\.tar\.xz' "$tmp/SHASUMS256.txt" | head -1) ;;
    aarch64) file=$(grep -oE 'node-v[0-9.]+-linux-arm64\.tar\.xz' "$tmp/SHASUMS256.txt" | head -1) ;;
  esac
  [[ -n $file ]] || die "cannot resolve a Node ${want} tarball"
  sum=$(grep " $file\$" "$tmp/SHASUMS256.txt" | awk '{print $1}')
  fetch "$base/$file" "$tmp/$file"
  echo "$sum  $tmp/$file" | sha256sum -c - >/dev/null || die "Node tarball checksum mismatch"
  mkdir -p "$HOME/.local/lib"
  tar -xJf "$tmp/$file" -C "$HOME/.local/lib"
  mkdir -p "$HOME/.local/bin"
  ln -sfn "$HOME/.local/lib/${file%.tar.xz}/bin/node" "$HOME/.local/bin/node"
  ln -sfn "$HOME/.local/lib/${file%.tar.xz}/bin/npm" "$HOME/.local/bin/npm"
  ln -sfn "$HOME/.local/lib/${file%.tar.xz}/bin/npx" "$HOME/.local/bin/npx"
  rm -rf "$tmp"
}

install_go() {
  local want json file sum tmp
  want=$(pin go)
  tmp=$(mktemp -d)
  json=$(curl -fsSL "https://go.dev/dl/?mode=json&include=all")
  case $(uname -m) in
    x86_64) file=$(printf '%s' "$json" | python3 -c '
import json,sys
want=sys.argv[1]
rows=json.load(sys.stdin)
for rel in rows:
    if rel.get("stable") and rel["version"].startswith("go"+want.split(".")[0]+"."+want.split(".")[1]+"."):
        for f in rel["files"]:
            if f["os"]=="linux" and f["arch"]=="amd64" and f["kind"]=="archive":
                print(f["filename"], f["sha256"]); raise SystemExit
' "$want") ;;
    aarch64) file=$(printf '%s' "$json" | python3 -c '
import json,sys
want=sys.argv[1]
rows=json.load(sys.stdin)
for rel in rows:
    if rel.get("stable") and rel["version"].startswith("go"+want.split(".")[0]+"."+want.split(".")[1]+"."):
        for f in rel["files"]:
            if f["os"]=="linux" and f["arch"]=="arm64" and f["kind"]=="archive":
                print(f["filename"], f["sha256"]); raise SystemExit
' "$want") ;;
  esac
  [[ -n $file ]] || die "cannot resolve a Go ${want} tarball"
  set -- $file
  fetch "https://go.dev/dl/$1" "$tmp/$1"
  echo "$2  $tmp/$1" | sha256sum -c - >/dev/null || die "Go tarball checksum mismatch"
  mkdir -p "$HOME/.local/lib"
  tar -xzf "$tmp/$1" -C "$HOME/.local/lib"
  mkdir -p "$HOME/.local/bin"
  ln -sfn "$HOME/.local/lib/go/bin/go" "$HOME/.local/bin/go"
  ln -sfn "$HOME/.local/lib/go/bin/gofmt" "$HOME/.local/bin/gofmt"
  rm -rf "$tmp"
}

install_rtk() {
  local url sha tmp name
  case $(uname -m) in
    x86_64) url=$(pin rtk_url); sha=$(pin rtk_sha256) ;;
    aarch64) url=$(pin rtk_url_arm64); sha=$(pin rtk_sha256_arm64) ;;
    *) die "unsupported architecture for rtk" ;;
  esac
  tmp=$(mktemp -d)
  fetch "$url" "$tmp/rtk.tar.gz"
  echo "$sha  $tmp/rtk.tar.gz" | sha256sum -c - >/dev/null || die "rtk checksum mismatch"
  tar -xzf "$tmp/rtk.tar.gz" -C "$tmp"
  name=$(find "$tmp" -maxdepth 2 -type f -name rtk | head -1)
  [[ -n $name ]] || die "rtk archive has no rtk binary"
  mkdir -p "$HOME/.local/bin"
  install -m 0755 "$name" "$HOME/.local/bin/rtk"
  rm -rf "$tmp"
}

ARTIFACT_PATH=
ARTIFACT_TMP=

artifact_source() {
  local version arch url sha tmp
  version=$(pin fleet_console)
  case $(uname -m) in
    x86_64) arch=amd64 ;;
    aarch64) arch=arm64 ;;
    *) die "unsupported architecture" ;;
  esac
  if [[ -n $APP_BINARY ]]; then
    [[ -x $APP_BINARY ]] || die "--app-binary is not executable: $APP_BINARY"
    emit "app: using local binary $APP_BINARY sha256=$(sha256_of "$APP_BINARY") (release tarball pin not checked)"
    ARTIFACT_PATH=$APP_BINARY
    return
  fi
  if [[ -z $APP_TARBALL ]]; then
    for candidate in \
      "$SELF_DIR/fleet-console-$version-linux-$arch.tar.gz" \
      "$SELF_DIR/fleet-console-$version-linux-$arch.tar.gz"; do
      if [[ -f $candidate ]]; then APP_TARBALL=$candidate; break; fi
    done
  fi
  if [[ -z $APP_TARBALL ]]; then
    url=${LOCK[fleet_console_url]:-}
    [[ -n $url ]] || die "no app artifact: pass --app-tarball, --app-binary or set fleet_console_url in stack.lock"
    APP_TARBALL=$(mktemp)
    fetch "$url" "$APP_TARBALL"
    ARTIFACT_TMP=$APP_TARBALL
  fi
  [[ -f $APP_TARBALL ]] || die "app tarball not found: $APP_TARBALL"
  sha=$(pin fleet_console_sha256)
  if [[ $sha == pending ]]; then
    die "stack.lock has no fleet_console_sha256 yet; pass --app-binary for a development build"
  fi
  [[ $(sha256_of "$APP_TARBALL") == "$sha" ]] || die "app tarball sha256 mismatch (want $sha)"
  tmp=$(mktemp -d)
  tar -xzf "$APP_TARBALL" -C "$tmp"
  if [[ ! -x $tmp/fleet-console ]]; then
    rm -rf "$tmp"
    die "app tarball has no fleet-console binary"
  fi
  emit "app: verified tarball sha256=$sha"
  local bin_sha=${LOCK[fleet_console_binary_sha256]:-}
  if [[ -n $bin_sha && $bin_sha != pending ]]; then
    [[ $(sha256_of "$tmp/fleet-console") == "$bin_sha" ]] || { rm -rf "$tmp"; die "app binary sha256 mismatch (want $bin_sha)"; }
  fi
  ARTIFACT_TMP=$tmp
  ARTIFACT_PATH=$tmp/fleet-console
}

install_app() {
  local src target tmp changed=0
  mkdir -p "$HOME/.local/bin" "$HOME/.config/fleet-console" "$HOME/.local/state/fleet-console"
  artifact_source
  src=$ARTIFACT_PATH
  target="$HOME/.local/bin/fleet-console"
  if [[ ! -f $target ]] || [[ $(sha256_of "$src") != $(sha256_of "$target") ]]; then
    tmp=$(mktemp)
    install -m 0755 "$src" "$tmp"
    mv "$tmp" "$target"
    changed=1
  fi
  if ! env_file_satisfied; then
    local envf="$HOME/.config/fleet-console/fleet-console.env"
    (umask 077; {
      echo "FLEET_CONSOLE_TOKEN=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    } >"$envf")
    chmod 600 "$envf"
    changed=1
  fi
  if ! unit_file_satisfied fleet-console; then
    mkdir -p "$HOME/.config/systemd/user"
    local dst="$HOME/.config/systemd/user/fleet-console.service"
    [[ -f $dst ]] && cp -p "$dst" "$dst.bak-$(date -u +%Y%m%dT%H%M%SZ)"
    install -m 0644 "$SELF_DIR/deploy/fleet-console.service" "$dst"
    changed=1
  fi
  mkdir -p "$HOME/.local/share/fleet-console/templates"
  install -m 0644 "$SELF_DIR/deploy/rig.yaml.tmpl" "$HOME/.local/share/fleet-console/templates/rig.yaml.tmpl"
  [[ -n $ARTIFACT_TMP ]] && rm -rf "$ARTIFACT_TMP"
  ARTIFACT_TMP=
  if ((changed)); then
    systemctl --user daemon-reload 2>/dev/null || true
    if systemctl --user is-active fleet-console >/dev/null 2>&1; then
      systemctl --user restart fleet-console
    fi
  fi
}

install_units() {
  mkdir -p "$HOME/.config/systemd/user"
  systemctl --user daemon-reload
  if [[ $PROFILE == fleet ]]; then
    if rig daemon status >/dev/null 2>&1 && ! unit_state rig-daemon; then
      emit "units: rig daemon is already running outside systemd; enabling the unit without starting it"
      systemctl --user enable rig-daemon
    else
      systemctl --user enable --now rig-daemon
    fi
  fi
  systemctl --user enable --now fleet-console
  if ! loginctl show-user "$(id -u)" -p Linger 2>/dev/null | grep -qx 'Linger=yes'; then
    loginctl enable-linger "$(id -u)"
  fi
}

apply_step() {
  local name=$1
  case $name in
    preflight) preflight ;;
    path)
      if merge_block "$HOME/.profile" "$(path_block)"; then
        emit "path: marker block written to ~/.profile"
      fi
      ;;
    packages)
      local missing=() p
      for p in git tmux jq curl unzip sqlite3; do
        command -v "$p" >/dev/null 2>&1 || missing+=("$p")
      done
      package_installed ca-certificates || missing+=(ca-certificates)
      if ((${#missing[@]})); then
        require_root_or_sudo
        if [[ $EUID -eq 0 ]]; then
          DEBIAN_FRONTEND=noninteractive apt-get update
        else
          sudo DEBIAN_FRONTEND=noninteractive apt-get update
        fi
        apt_install "${missing[@]}"
      fi
      version_matches "$(pin node)" "$(installed_version node || true)" || install_node
      version_matches "$(pin go)" "$(installed_version go || true)" || install_go
      emit "packages: node $(installed_version node) go $(installed_version go)"
      ;;
    openrig)
      mkdir -p "$HOME/.local/bin"
      npm install -g --prefix "$HOME/.local" "@openrig/cli@$(pin openrig)"
      ;;
    rigsetup)
      rig setup --dry-run || true
      rig setup || emit "rigsetup: rig setup reported findings; \`rig doctor\` explains them"
      ;;
    tooling)
      version_matches "$(pin serena)" "$(installed_version serena || true)" ||
        uv tool install --force "serena-agent==$(pin serena)"
      version_matches "$(pin gopls)" "$(installed_version gopls || true)" ||
        go install "golang.org/x/tools/gopls@v$(pin gopls)"
      version_matches "$(pin codegraph)" "$(installed_version codegraph || true)" ||
        npm install -g --prefix "$HOME/.local" "@colbymchenry/codegraph@$(pin codegraph)"
      version_matches "$(pin rtk)" "$(installed_version rtk || true)" || install_rtk
      ;;
    harness_stop)
      harness_ready || human_stop "sign in to omp (run: omp, or import credentials with omp auth import)"
      ;;
    gh_stop) gh auth status >/dev/null 2>&1 || human_stop "run: gh auth login" ;;
    glab_stop) glab auth status >/dev/null 2>&1 || human_stop "run: glab auth login" ;;
    seat_stop) seat_key_present || human_stop "export OMP_PROVIDER_KEY=... for the seat credentials (never logged)" ;;
    app) install_app ;;
    units) install_units ;;
    *) die "unknown step $name" ;;
  esac
}

# ------------------------------------------------------------------- verify

verify_fail() {
  emit "fail $1: $2; rerun: $3"
  FAILED=$((FAILED + 1))
}

verify_ok() { emit "ok $1: $2"; }

verify_versions() {
  local key want got
  for key in $(step_keys "$PROFILE"); do
    want=$(pin "$key")
    if ! got=$(installed_version "$key"); then
      verify_fail "version:$key" "not installed" "./install.sh --apply --profile $PROFILE"
    elif version_matches "$want" "$got"; then
      verify_ok "version:$key" "$got (pin $want)"
    else
      verify_fail "version:$key" "installed $got, want $want" "./install.sh --apply --profile $PROFILE"
    fi
  done
}

verify_doctor() {
  command -v rig >/dev/null 2>&1 || { verify_fail doctor "rig is missing" "install rig"; return; }
  local unexpected
  unexpected=$(rig doctor --json 2>/dev/null | python3 -c '
import json,sys
allowed={"codex_install","codex_auth","cmux_shell","spec_live_conformance","claude_install","claude_auth"}
try:
    doc=json.load(sys.stdin)
except Exception:
    print("unreadable"); raise SystemExit
bad=[c["name"]+"="+c["status"] for c in doc.get("checks",[]) if c.get("status") not in ("pass","skip") and c.get("name") not in allowed]
print(" ".join(bad))
' || true)
  if [[ -n $unexpected ]]; then
    verify_fail doctor "unexpected findings: $unexpected" "rig doctor"
  else
    verify_ok doctor "only the expected optional-harness/cmux findings"
  fi
}

verify_daemon() {
  if rig daemon status 2>/dev/null | grep -qi running; then
    verify_ok daemon "running"
  else
    verify_fail daemon "daemon is not running" "rig daemon start"
  fi
}

verify_forge() {
  if gh api user >/dev/null 2>&1; then
    verify_ok forge "gh api user"
  elif glab api user >/dev/null 2>&1; then
    verify_ok forge "glab api user"
  else
    verify_fail forge "no authenticated forge read" "gh auth login (or glab auth login)"
  fi
}

verify_compat() {
  local out="$HOME/.local/bin/fleet-console"
  command -v "$out" >/dev/null 2>&1 || { verify_fail compat "binary missing" "./install.sh --apply --profile $PROFILE"; return; }
  if ! "$out" --compat >/tmp/fleet-compat.$$ 2>&1; then
    verify_fail compat "compat probe failed: $(tail -1 /tmp/fleet-compat.$$)" "$out --compat"
    rm -f /tmp/fleet-compat.$$
    return
  fi
  local version floor
  version=$(sed -n 's/.*"version": "\([^"]*\)".*/\1/p' /tmp/fleet-compat.$$ | head -1)
  floor=$(sed -n 's/.*"floor": "\([^"]*\)".*/\1/p' /tmp/fleet-compat.$$ | head -1)
  rm -f /tmp/fleet-compat.$$
  verify_ok compat "rig ${version:-?} floor ${floor:-?}"
}

wait_health() {
  local i
  for i in $(seq 1 50); do
    curl -fsS --max-time 1 http://127.0.0.1:8787/api/health >/dev/null 2>&1 && return 0
    sleep 0.2
  done
  return 1
}

verify_health() {
  if wait_health; then
    verify_ok health "GET /api/health 200"
  else
    verify_fail health "GET /api/health did not answer 200" "systemctl --user restart fleet-console"
  fi
}

verify_units() {
  local name
  for name in fleet-console; do
    if unit_state "$name"; then
      verify_ok "unit:$name" "active and enabled"
    else
      verify_fail "unit:$name" "not active+enabled" "systemctl --user enable --now $name"
    fi
  done
  if [[ $PROFILE == fleet ]]; then
    if unit_state rig-daemon; then
      verify_ok "unit:rig-daemon" "active and enabled"
    elif rig daemon status 2>/dev/null | grep -qi running; then
      verify_ok "unit:rig-daemon" "daemon running outside the unit (pre-existing)"
    else
      verify_fail "unit:rig-daemon" "not active+enabled" "systemctl --user enable --now rig-daemon"
    fi
  fi
  if loginctl show-user "$(id -u)" -p Linger 2>/dev/null | grep -qx 'Linger=yes'; then
    verify_ok linger "enabled"
  else
    verify_fail linger "linger is off" "loginctl enable-linger $(id -u)"
  fi
}

verify_appfiles() {
  local envf="$HOME/.config/fleet-console/fleet-console.env" target="$HOME/.local/bin/fleet-console"
  if [[ -f $envf && $(stat -c '%a' "$envf") == 600 ]]; then
    verify_ok "envfile" "mode 600"
  else
    verify_fail "envfile" "missing or not mode 600" "./install.sh --apply --profile $PROFILE"
  fi
  if [[ -x $target ]]; then
    local got want
    got=$(version_of_binary "$target")
    want=$(pin fleet_console)
    if [[ $got == "$want" ]]; then
      verify_ok "app-version" "$got"
    else
      verify_fail "app-version" "installed ${got:-unreadable}, want $want" "./install.sh --apply --profile $PROFILE"
    fi
  else
    verify_fail "app-version" "binary missing" "./install.sh --apply --profile $PROFILE"
  fi
}

verify_all() {
  preflight
  verify_versions
  verify_doctor
  verify_daemon
  verify_forge
  verify_compat
  verify_health
  verify_units
  verify_appfiles
  if ((FAILED > 0)); then
    emit "verify: $FAILED check(s) failed"
    exit 1
  fi
  emit "verify: all checks passed"
}

# ------------------------------------------------------------------ upgrade

upgrade() {
  local target="$HOME/.local/bin/fleet-console"
  local db="$HOME/.local/state/fleet-console/fleet-console.db"
  local ts new backup prev
  [[ -x $target ]] || die "no installed binary at $target; run --apply first"
  ts=$(date -u +%Y%m%dT%H%M%SZ)
  artifact_source
  new=$ARTIFACT_PATH
  backup="$db.bak-$ts"
  prev="$target.bak-$ts"
  mkdir -p "$(dirname "$db")"
  systemctl --user stop fleet-console 2>/dev/null || true
  if [[ -f $db ]]; then cp -p "$db" "$backup"; fi
  if ! "$new" --migrate --db "$db"; then
    systemctl --user start fleet-console 2>/dev/null || true
    emit "upgrade: migration failed; previous binary untouched; database backup $backup"
    [[ -n $ARTIFACT_TMP ]] && rm -rf "$ARTIFACT_TMP"
    exit 1
  fi
  cp -p "$target" "$prev"
  install -m 0755 "$new" "$target"
  [[ -n $ARTIFACT_TMP ]] && rm -rf "$ARTIFACT_TMP"
  systemctl --user start fleet-console
  if ! wait_health; then
    install -m 0755 "$prev" "$target"
    systemctl --user restart fleet-console
    emit "upgrade: health check failed; previous binary restored; database backup $backup"
    exit 1
  fi
  emit "upgrade: done; database backup $backup"
}

# --------------------------------------------------------------------- main

load_lock

if [[ $ACTION == verify ]]; then
  log_start
  verify_all
  exit 0
fi

if [[ $ACTION == upgrade ]]; then
  log_start
  upgrade
  exit 0
fi

if [[ $MODE == dry-run ]]; then
  emit "profile: $PROFILE"
  emit "mode: dry-run (writes nothing)"
  emit "lock: $LOCK_FILE"
  emit "pins:"
  for key in $(step_keys "$PROFILE"); do
    emit "  $key = $(pin "$key") ($(version_state "$key"))"
  done
  preflight
  emit "steps:"
  for name in $(step_list); do
    STEP_NO=$((STEP_NO + 1))
    emit "  $STEP_NO. $name: $(step_desc "$name")"
  done
  exit 0
fi

preflight
log_start
for name in $(step_list); do
  STEP_NO=$((STEP_NO + 1))
  if [[ $name == verify ]]; then
    emit "step $STEP_NO: verify: run ./install.sh --verify when the human steps are done"
    continue
  fi
  if step_satisfied "$name"; then
    emit "step $STEP_NO: $name: already satisfied"
    continue
  fi
  emit "step $STEP_NO: $name: $(step_desc "$name")"
  apply_step "$name"
  if ! step_satisfied "$name"; then
    die "step $STEP_NO: $name did not satisfy its own check"
  fi
  emit "step $STEP_NO: $name: ok"
done
emit "apply: $STEP_NO steps, all satisfied"
