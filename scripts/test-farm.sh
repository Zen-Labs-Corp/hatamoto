#!/usr/bin/env bash
# test-farm.sh - offline test of .githooks/lib/farm.sh (the host-wide farm slot limit).
# Run by `make check`. Uses its own HATAMOTO_FARM_DIR, never ~/.cache.
set -euo pipefail
top=$(git rev-parse --show-toplevel)
lib=$top/.githooks/lib/farm.sh
T=$(mktemp -d)
pids=()
cleanup() { local p; for p in "${pids[@]}"; do kill -9 "$p" 2>/dev/null || true; done; rm -rf "$T"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
export HATAMOTO_FARM_DIR=$T/farm HATAMOTO_FARM_POLL=0.2
unset HATAMOTO_FARM_SLOT_HELD

# acq <name> [env...]: a process that takes a slot, logs "got <name>", and holds it until
# $T/release.<name> exists (then logs "done <name>"). Output goes to $T/out.<name>.
acq() {
  local name=$1; shift
  env HATAMOTO_FARM_BRANCH="br-$name" "$@" bash -c '
    source "$1"; hm_farm_acquire "test $2"
    echo "got $2" >>"$3/events"
    while [[ ! -e $3/release.$2 ]]; do sleep 0.1; done
    echo "done $2" >>"$3/events"' _ "$lib" "$name" "$T" >"$T/out.$name" 2>&1 &
  pids+=($!)
  LAST_PID=$!
}
waitfor() { # <description> <grep -E pattern> <file> [seconds]
  local i
  for ((i = 0; i < ${4:-100}; i++)); do grep -qE "$2" "$3" 2>/dev/null && return 0; sleep 0.1; done
  fail "timed out waiting for $1 (pattern '$2' in $3)"
}
: >"$T/events"

# --- 3 acquirers, 2 slots: the third waits, names the holder, then runs after a release ---
export HATAMOTO_FARM_SLOTS=2
acq A; waitfor "A holds a slot" '^got A$' "$T/events"
acq B; waitfor "B holds a slot" '^got B$' "$T/events"
acq C; waitfor "C to report waiting" 'waiting behind' "$T/out.C"
grep -q 'waiting behind br-A (test A' "$T/out.C" || fail "the waiter must name the holder: $(cat "$T/out.C")"
grep -q '^got C$' "$T/events" && fail "C ran while both slots were held"
grep -q 'farm slot 1/2' "$T/out.A" || fail "A should say which slot it took: $(cat "$T/out.A")"
grep -q 'waiting behind' "$T/out.A" "$T/out.B" && fail "A and B must not wait"
touch "$T/release.A"
waitfor "C to run once A released" '^got C$' "$T/events"
grep -q 'waited' "$T/out.C" || fail "C should report how long it waited: $(cat "$T/out.C")"
touch "$T/release.B" "$T/release.C"; wait "${pids[@]}" 2>/dev/null || true; pids=()
# concurrency never exceeded the slots
max=0 cur=0
while read -r ev _; do
  case $ev in got) cur=$((cur + 1)) ;; done) cur=$((cur - 1)) ;; esac
  ((cur > max)) && max=$cur
done <"$T/events"
((max <= 2)) || fail "$max holders at once, the limit is 2"
ls "$T/farm"/slot-*.holder >/dev/null 2>&1 && fail "holder files are left after every release: $(ls "$T/farm")"

# --- a holder killed with SIGKILL frees its slot (flock is released by the kernel) ---
: >"$T/events"; export HATAMOTO_FARM_SLOTS=1
acq K; waitfor "K holds the slot" '^got K$' "$T/events"
kill -9 "$LAST_PID"; wait "$LAST_PID" 2>/dev/null || true
acq L; waitfor "L to take the slot K left" '^got L$' "$T/events"
touch "$T/release.L"; wait "$LAST_PID" 2>/dev/null || true

# --- the held marker: a nested hook never waits, even when every slot is taken ---
acq M; waitfor "M holds the slot" '^got M$' "$T/events"
out=$(HATAMOTO_FARM_SLOT_HELD=1 timeout 5 bash -c 'source "$1"; hm_farm_acquire nested; echo returned' _ "$lib" 2>&1) \
  || fail "a nested acquire with the marker blocked: $out"
[[ $out == returned ]] || fail "a nested acquire must be silent: $out"
# hm_farm_release exports nothing wrong: a holder marks its children
out=$(bash -c 'source "$1"; HATAMOTO_FARM_SLOTS=3 hm_farm_acquire x 2>/dev/null; echo "held=$HATAMOTO_FARM_SLOT_HELD"; hm_farm_release; echo "after=${HATAMOTO_FARM_SLOT_HELD:-unset}"' _ "$lib")
[[ $out == $'held=1\nafter=unset' ]] || fail "the holder must export the marker and drop it on release: $out"
touch "$T/release.M"; wait "$LAST_PID" 2>/dev/null || true

# --- the mkdir fallback (no flock), including stale pids ---
export HATAMOTO_FARM_NO_FLOCK=1 HATAMOTO_FARM_SLOTS=1
rm -rf "$T/farm"; : >"$T/events"
acq N; waitfor "N holds the slot (mkdir)" '^got N$' "$T/events"
[[ -d $T/farm/slot-1.d ]] || fail "the fallback should use a slot directory: $(ls "$T/farm")"
acq O; waitfor "O to wait (mkdir)" 'waiting behind br-N' "$T/out.O"
grep -q '^got O$' "$T/events" && fail "O ran while N held the slot (mkdir)"
touch "$T/release.N"; waitfor "O to run after N (mkdir)" '^got O$' "$T/events"
touch "$T/release.O"; wait "${pids[@]}" 2>/dev/null || true; pids=()
[[ ! -d $T/farm/slot-1.d ]] || fail "the slot directory is left after release"
# a slot whose pid is dead is broken
dead=$(bash -c 'echo $$')
mkdir -p "$T/farm/slot-1.d"; echo "$dead" >"$T/farm/slot-1.d/pid"
out=$(timeout 10 bash -c 'source "$1"; hm_farm_acquire stale; hm_farm_release' _ "$lib" 2>&1) || fail "a stale slot was not broken: $out"
grep -q 'farm slot 1/1$' <<<"$out" || fail "the stale slot should be taken without waiting: $out"
grep -q waiting <<<"$out" && fail "waited for a dead pid: $out"
# a live pid is not
sleep 30 & live=$!; pids+=("$live")
mkdir -p "$T/farm/slot-1.d"; echo "$live" >"$T/farm/slot-1.d/pid"
out=$(timeout 2 bash -c 'source "$1"; hm_farm_acquire live' _ "$lib" 2>&1) && fail "took a slot whose pid is alive"
kill "$live"
[[ -d $T/farm/slot-1.d ]] || fail "a live slot was removed"
rm -rf "$T/farm/slot-1.d"
# a live pid with the recorded start time is held; the same pid with another start time was reused: stale
sleep 30 & live=$!; pids+=("$live")
source "$lib"
start=$(_hm_farm_start "$live"); [[ -n $start ]] || fail "cannot read the start time of a live process"
mkdir -p "$T/farm/slot-1.d"; printf '%s\n%s\n' "$live" "$start" >"$T/farm/slot-1.d/pid"
out=$(timeout 2 bash -c 'source "$1"; hm_farm_acquire live' _ "$lib" 2>&1) && fail "took a slot whose pid and start time match"
printf '%s\n%s\n' "$live" "$start-reused" >"$T/farm/slot-1.d/pid"
out=$(timeout 10 bash -c 'source "$1"; hm_farm_acquire reused; hm_farm_release' _ "$lib" 2>&1) || fail "a slot whose pid was reused was not broken: $out"
grep -q 'farm slot 1/1$' <<<"$out" && ! grep -q waiting <<<"$out" || fail "the reused-pid slot should be taken without waiting: $out"
kill "$live"
# a holder records its pid and start time
out=$(bash -c 'source "$1"; hm_farm_acquire rec 2>/dev/null; cat "$HATAMOTO_FARM_DIR/slot-1.d/pid"; grep "^start=" "$HATAMOTO_FARM_DIR/slot-1.holder"; echo "me=$$"' _ "$lib")
[[ $(sed -n 1p <<<"$out") == "$(sed -n 4p <<<"$out" | cut -d= -f2)" && -n $(sed -n 2p <<<"$out") && $(sed -n 3p <<<"$out") == "start=$(sed -n 2p <<<"$out")" ]] \
  || fail "the slot must record pid and start time in the pid and holder files: $out"
# putting a slot back never nests into one somebody took meanwhile
rm -rf "$T/farm"; mkdir -p "$T/farm/slot-1.stale.9" "$T/farm/slot-1.d"; echo 111 >"$T/farm/slot-1.stale.9/pid"; echo 222 >"$T/farm/slot-1.d/pid"
_hm_farm_putback "$T/farm/slot-1.stale.9" "$T/farm/slot-1.d"
[[ $(cat "$T/farm/slot-1.d/pid") == 222 && -z $(ls "$T/farm/slot-1.d" | grep -v '^pid$') && ! -e $T/farm/slot-1.stale.9 ]] \
  || fail "put-back nested into or replaced a taken slot: $(find "$T/farm")"
rm -rf "$T/farm/slot-1.d"; mkdir -p "$T/farm/slot-1.stale.9"; echo 111 >"$T/farm/slot-1.stale.9/pid"
_hm_farm_putback "$T/farm/slot-1.stale.9" "$T/farm/slot-1.d"
[[ $(cat "$T/farm/slot-1.d/pid") == 111 && ! -e $T/farm/slot-1.stale.9 ]] || fail "put-back of a free slot lost it: $(find "$T/farm")"
# a slot directory with no pid file yet (a slot being taken) is waited on without shell noise
rm -rf "$T/farm"; mkdir -p "$T/farm/slot-1.d"
out=$(timeout 2 bash -c 'source "$1"; hm_farm_acquire noisy' _ "$lib" 2>&1 >/dev/null) || true
grep -q 'No such file' <<<"$out" && fail "a missing pid file printed an error: $out"
echo "test-farm: ok"
