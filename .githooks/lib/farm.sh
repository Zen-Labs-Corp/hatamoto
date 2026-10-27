#!/usr/bin/env bash
# Sourced by pre-push and scripts/ship.sh.
#
# A host-wide limit on heavy gate phases, so several pushes and ships do not pile onto the
# build farm at once. HATAMOTO_FARM_SLOTS slots (default 2); each is a lock file under
# HATAMOTO_FARM_DIR (default ${XDG_CACHE_HOME:-$HOME/.cache}/hatamoto-farm). Take a slot only
# when there is uncached work, never for the quick gates:
#
#   hm_farm_acquire "pre-push $branch"   # waits for a free slot, printing who it waits behind
#   ...heavy phase...                    # gate launchers must start with 200>&- (see below)
#   hm_farm_release                      # also runs on EXIT, INT, TERM and HUP (unless the caller traps them)
#
# The holder exports HATAMOTO_FARM_SLOT_HELD=1, so a hook nested in the heavy phase (make check
# runs scripts/test-hooks.sh, which pushes in scratch repos) never waits for the slot its
# own parent holds. Release is automatic when the holder dies: flock is released by the
# kernel; the mkdir fallback (no flock, e.g. macOS) breaks a slot whose pid is dead or was
# reused by another process (the holder file records the pid and its start time).
#
# The slot lives on file descriptor 200. A child that inherits it keeps the slot after the
# holder exits, so background daemons and gate launchers close it: `cmd 200>&-`.
#
# HATAMOTO_FARM_POLL (seconds, default 2) is the wait poll; HATAMOTO_FARM_BRANCH names the
# holder in "waiting behind" messages (default: the current git branch).

HM_FARM_SLOT=
HM_FARM_KIND=

hm_farm_dir() { printf '%s\n' "${HATAMOTO_FARM_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/hatamoto-farm}"; }

_hm_farm_alive() { [[ -n ${1:-} ]] && kill -0 "$1" 2>/dev/null; }

# _hm_farm_start <pid>: a token that identifies the process for its whole life (the start time:
# /proc/<pid>/stat field 22, else ps), empty when it cannot be read.
_hm_farm_start() {
  local s
  local -a f
  if [[ -r /proc/$1/stat ]]; then
    s=$(cat "/proc/$1/stat" 2>/dev/null) || s=
    read -ra f <<<"${s##*) }"
    printf '%s\n' "${f[19]:-}"
  else
    ps -o lstart= -p "$1" 2>/dev/null | tr -s ' ' | sed 's/^ //' || true
  fi
}

# _hm_farm_holder_alive <pid> <recorded start time>: the recorded holder still runs. A pid whose
# process started at another time was reused. A start time that was not recorded, or cannot be
# read now, is not proof of death: the slot stays held.
_hm_farm_holder_alive() {
  local now
  _hm_farm_alive "${1:-}" || return 1
  [[ -n ${2:-} ]] || return 0
  now=$(_hm_farm_start "$1")
  [[ -z $now || $now == "$2" ]]
}

# _hm_farm_write_pid <slot dir>: record this process (pid, then start time).
_hm_farm_write_pid() { printf '%s\n%s\n' "$$" "$(_hm_farm_start "$$")" >"$1/pid"; }

# _hm_farm_putback <stale dir> <slot dir>: give back a slot that was moved aside by mistake. mkdir on
# the exact path, so it never nests into a slot somebody took meanwhile (then it is dropped).
_hm_farm_putback() {
  if mkdir "$2" 2>/dev/null; then
    mv "$1/pid" "$2/pid" 2>/dev/null || true
  fi
  rm -rf "$1"
}

# _hm_farm_holder_info <slot dir/file prefix>: "<branch> (<what>, <n>s ago)" or empty
_hm_farm_holder_info() {
  local f=$1.holder pid= start= branch= what= since= k v now
  [[ -f $f ]] || return 0
  while IFS='=' read -r k v; do
    case $k in pid) pid=$v ;; start) start=$v ;; branch) branch=$v ;; what) what=$v ;; since) since=$v ;; esac
  done <"$f"
  _hm_farm_holder_alive "$pid" "$start" || return 0
  now=$(date +%s)
  printf '%s (%s, %ss ago)\n' "${branch:-?}" "${what:-?}" "$((now - ${since:-$now}))"
}

# _hm_farm_try <slot number>: take the slot; 0 on success.
_hm_farm_try() {
  local i=$1 dir pid start
  dir=$(hm_farm_dir)
  if [[ -z ${HATAMOTO_FARM_NO_FLOCK:-} ]] && command -v flock >/dev/null 2>&1; then
    exec 200>>"$dir/slot-$i.lock"
    if flock -n 200; then HM_FARM_KIND=flock; return 0; fi
    exec 200>&-
    return 1
  fi
  if mkdir "$dir/slot-$i.d" 2>/dev/null; then
    _hm_farm_write_pid "$dir/slot-$i.d"; HM_FARM_KIND=mkdir; return 0
  fi
  # Held, or left behind by a holder that died. A pid file that is not written yet is a
  # slot being taken: leave it alone unless the directory is old.
  { { read -r pid; read -r start; } <"$dir/slot-$i.d/pid"; } 2>/dev/null || true
  if [[ -z $pid ]]; then
    [[ -d $dir/slot-$i.d && -z $(find "$dir/slot-$i.d" -maxdepth 0 -mmin -1 2>/dev/null) ]] || return 1
  elif _hm_farm_holder_alive "$pid" "$start"; then
    return 1
  fi
  # Stale: move it aside atomically (one breaker wins), and put it back if it turned out to be
  # somebody's fresh slot.
  if mv "$dir/slot-$i.d" "$dir/slot-$i.stale.$$" 2>/dev/null; then
    local got= gotstart=
    { read -r got; read -r gotstart; } <"$dir/slot-$i.stale.$$/pid" 2>/dev/null || true
    if [[ $got == "$pid" && $gotstart == "$start" ]]; then
      rm -rf "$dir/slot-$i.stale.$$"
    else
      _hm_farm_putback "$dir/slot-$i.stale.$$" "$dir/slot-$i.d"
      return 1
    fi
  fi
  if mkdir "$dir/slot-$i.d" 2>/dev/null; then
    _hm_farm_write_pid "$dir/slot-$i.d"; HM_FARM_KIND=mkdir; return 0
  fi
  return 1
}

# hm_farm_acquire <what>: block until a slot is free, then hold it. No-op when this process
# tree already holds one (HATAMOTO_FARM_SLOT_HELD=1).
hm_farm_acquire() {
  [[ ${HATAMOTO_FARM_SLOT_HELD:-} == 1 ]] && return 0
  local what=${1:-gate} n=${HATAMOTO_FARM_SLOTS:-2} dir i info last= waited= shown poll=${HATAMOTO_FARM_POLL:-2} branch t0
  [[ $n =~ ^[1-9][0-9]*$ ]] || n=2
  dir=$(hm_farm_dir)
  mkdir -p "$dir" || { echo "farm: cannot create $dir; not limiting" >&2; return 0; }
  branch=${HATAMOTO_FARM_BRANCH:-$(git branch --show-current 2>/dev/null || true)}
  t0=$(date +%s)
  while :; do
    for ((i = 1; i <= n; i++)); do
      if _hm_farm_try "$i"; then
        HM_FARM_SLOT=$i
        printf 'pid=%s\nstart=%s\nbranch=%s\nwhat=%s\nsince=%s\n' "$$" "$(_hm_farm_start "$$")" "${branch:-?}" "$what" "$(date +%s)" >"$dir/slot-$i.holder"
        export HATAMOTO_FARM_SLOT_HELD=1
        echo "farm slot $i/$n${waited:+ (waited $(($(date +%s) - t0))s)}" >&2
        _hm_farm_traps
        return 0
      fi
    done
    shown=
    for ((i = 1; i <= n; i++)); do
      info=$(_hm_farm_holder_info "$dir/slot-$i")
      [[ -z $info ]] || { shown=$info; break; }
    done
    [[ -n $shown ]] || shown="another push"
    if [[ $shown != "$last" ]]; then
      echo "farm: all $n slots busy, waiting behind $shown" >&2
      last=$shown waited=1
    fi
    sleep "$poll"
  done
}

# Release on exit and on the usual signals, except where the caller already traps them.
_hm_farm_traps() {
  [[ -n $(trap -p EXIT) ]] || trap hm_farm_release EXIT
  [[ -n $(trap -p INT) ]] || trap 'hm_farm_release; exit 130' INT
  [[ -n $(trap -p TERM) ]] || trap 'hm_farm_release; exit 143' TERM
  [[ -n $(trap -p HUP) ]] || trap 'hm_farm_release; exit 129' HUP
}

hm_farm_release() {
  [[ -n $HM_FARM_SLOT ]] || return 0
  local dir
  dir=$(hm_farm_dir)
  rm -f "$dir/slot-$HM_FARM_SLOT.holder"
  if [[ $HM_FARM_KIND == flock ]]; then exec 200>&-; else rm -rf "$dir/slot-$HM_FARM_SLOT.d"; fi
  HM_FARM_SLOT= HM_FARM_KIND=
  unset HATAMOTO_FARM_SLOT_HELD
  return 0
}
