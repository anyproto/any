#!/bin/zsh
# ppseq.sh: Power Profiler energy runs on the iPhone. The phone runs on
# battery (Power Profiler reports 0 %/hr while charging), reached over Wi-Fi.
# xctrace launches the app itself; system-wide recording drops off Wi-Fi
# after 1.6 s. A devicectl call first wakes the tunnel, else xctrace waits
# for the device "to boot" and times out.
# ppseq.sh [runs.zsh]: the run list (default pp_runs_eg2.zsh) is sourced; PP_DIR overrides out/power.
set -u
D=${IPHONE_UDID:?set IPHONE_UDID (xcrun devicectl list devices)}; B=${EMBEDBENCH_BUNDLE:?set EMBEDBENCH_BUNDLE}
RUNS=${1:-}; [[ -n $RUNS ]] && RUNS=${RUNS:A}
cd "${0:A:h}"
[[ -n $RUNS ]] || RUNS=$PWD/pp_runs_eg2.zsh
P=${PP_DIR:-out/power}; mkdir -p $P
wake() {
  xcrun devicectl device info details --device $D >/dev/null 2>&1
  for i in 1 2 3 4 5 6 7 8 9 10; do
    xcrun xctrace list devices 2>&1 | sed -n '/== Devices ==/,/Offline/p' | grep -q "$D" && return 0
    sleep 3; xcrun devicectl device info details --device $D >/dev/null 2>&1
  done
  return 1
}
rec() { # rec <name> <app args...>
  local name=$1; shift
  echo "$(date +%T) $name"
  for try in 1 2 3 4; do
    rm -rf $P/$name.trace
    wake || echo "  wake failed"
    xcrun xctrace record --instrument 'Power Profiler' --instrument 'Neural Engine' --device $D \
      --output $P/$name.trace --time-limit 15m --launch -- $B "$@" --out $name > $P/$name.xctrace.log 2>&1
    grep -q 'Target app exited' $P/$name.xctrace.log && break
    echo "  try $try failed: $(tail -2 $P/$name.xctrace.log | tr '\n' ' ')"; sleep 10
  done
  wake >/dev/null
  for ext in json docs.f32 truncated; do
    xcrun devicectl device copy from --device $D --domain-type appDataContainer --domain-identifier $B \
      --source Documents/out/$name.$ext --destination $P/$name.$ext >/dev/null 2>&1
  done
  ls $P/$name.json >/dev/null 2>&1 || echo "  no json for $name"
}
idle() { rec $1 --mode idle --idle-sec ${2:-90}; }
source $RUNS
echo "$(date +%T) PPSEQ DONE"
