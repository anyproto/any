#!/bin/zsh
# Power runs: whole-system power from AppleSmartBattery accumulators (no sudo).
# acc.log gets one line per poll (2 s): unix-time AccSystemLoad Count AccPowerIn PowerInCount AccBatt BattCount.
# runs.log gets "name start end".
# power_seq.sh [runs.zsh]: the run list (default power_runs_eg2.zsh) is sourced after an idle baseline;
# POWER_DIR overrides the output dir (default <data>/power).
S=${0:A:h}
RUNS=${1:A}
[[ -n ${1:-} ]] || RUNS=$S/power_runs_eg2.zsh
W=$(git -C "${0:A:h}" rev-parse --show-toplevel)
B=${EMBEDBENCH_DIR:-$W/bench}
P=${POWER_DIR:-$B/power}
mkdir -p $P
cd $B
cp -f fiqa-1500.jsonl $P/
for k in 2 3 5 10; do
  [[ -f $P/fiqa-x$k.jsonl ]] || for i in $(seq $k); do cat fiqa-1500.jsonl; done > $P/fiqa-x$k.jsonl
done
: > $P/acc.log; : > $P/runs.log
(
  while true; do
    v=$(ioreg -rw0 -c AppleSmartBattery | grep -oE '"PowerTelemetryData" = \{[^}]*\}' | tr ',' '\n' | tr -d '"{}' )
    tget() { echo "$v" | grep -E "^ *$1=" | head -1 | cut -d= -f2; }
    echo "$(date +%s) $(tget AccumulatedSystemLoad) $(tget SystemLoadAccumulatorCount) $(tget AccumulatedSystemPowerIn) $(tget SystemPowerInAccumulatorCount) $(tget AccumulatedBatteryPower) $(tget BatteryPowerAccumulatorCount)" >> $P/acc.log
    sleep 2
  done
) &
poller=$!
trap "kill $poller 2>/dev/null" EXIT
mark() { echo "$1 $2 $(date +%s)" >> $P/runs.log; }

PY=$B/coreml/.venv/bin/python
TOK=coreml/filbona/tokenizer.json
go_run() { # name model kind be corpus [threads]
  local name=$1 model=$2 kind=$3 be=$4 corpus=$5 thr=${6:-}
  local st=$(date +%s)
  ANY_BENCH_CORPUS_OVERRIDE=$P/$corpus $S/run.sh $P/$name $model $kind $be $thr
  mark $name $st
  sleep 60
}
ml_run() { # name corpus model tokenizer opts...
  local name=$1 corpus=$2 model=$3 tok=$4; shift 4
  local st=$(date +%s)
  $PY -I $S/coreml_bench.py $model $tok $P/$corpus $P/$name "$@" --batch 1 --skip-queries > $P/$name.log 2>&1
  mark $name $st
  sleep 60
}

sleep 30
st=$(date +%s); sleep 300; mark idle $st
source $RUNS
st=$(date +%s); sleep 180; mark idle2 $st
echo DONE >> $P/runs.log
