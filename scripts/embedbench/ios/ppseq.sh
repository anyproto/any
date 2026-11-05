#!/bin/zsh
# ppseq.sh: Power Profiler energy runs on the iPhone. The phone runs on
# battery (Power Profiler reports 0 %/hr while charging), reached over Wi-Fi.
# xctrace launches the app itself; system-wide recording drops off Wi-Fi
# after 1.6 s. A devicectl call first wakes the tunnel, else xctrace waits
# for the device "to boot" and times out.
set -u
D=${IPHONE_UDID:?set IPHONE_UDID (xcrun devicectl list devices)}; B=${EMBEDBENCH_BUNDLE:?set EMBEDBENCH_BUNDLE}
cd "${0:A:h}"
P=out/power; mkdir -p $P
NM=(--model models/Nemosine.mlmodelc --function-fmt s%d --mask-input attention_mask)
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
idle pp-idle-0 90
rec pp-nemosine4 --mode coreml $NM --units ane --buckets 128,256,512,1024 --batch 1 --skip-queries --repeat 10
idle pp-idle-1 120
rec pp-eg2-q8-metal --mode llama --model gguf/embeddinggemma-2-Q8_0.gguf --kind eg2 --skip-queries --repeat 4
idle pp-idle-2 120
rec pp-qwen3-q8-metal --mode llama --model gguf/Qwen3-Embedding-0.6B-Q8_0.gguf --kind qwen3 --skip-queries --repeat 2
idle pp-idle-3 90
echo "$(date +%T) PPSEQ DONE"
