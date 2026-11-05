#!/bin/zsh
# irun.sh <name> <app args...>: launch EmbedBench on the iPhone with --out <name>,
# stream its console to out/<name>.log, then pull out/<name>.* back.
set -u
D=${IPHONE_UDID:?set IPHONE_UDID (xcrun devicectl list devices)}; B=${EMBEDBENCH_BUNDLE:?set EMBEDBENCH_BUNDLE}
cd "${0:A:h}"
name=$1; shift
for try in 1 2 3 4 5; do
  xcrun devicectl device process launch --device $D --console --terminate-existing $B -- "$@" --out $name > out/$name.log 2>&1
  rc=$?
  grep -q '^START' out/$name.log && break
  echo "$(date +%T) $name launch failed (try $try): $(grep -m1 -E 'Reason|operation couldn' out/$name.log)" >&2
  sleep 15
done
echo "launch exit=$rc" >> out/$name.log
for ext in json docs.f32 queries.f32 truncated; do
  xcrun devicectl device copy from --device $D --domain-type appDataContainer --domain-identifier $B \
    --source Documents/out/$name.$ext --destination out/$name.$ext >/dev/null 2>&1
done
grep -E '^(ERROR|DONE)|launch exit' out/$name.log | head -3
