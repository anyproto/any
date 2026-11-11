#!/system/bin/sh
# Energy per embedding from the battery charge counter (uAh) and voltage (mV); cable unplugged, screen off.
# idle phase first; net J/doc = (run drain - idle rate x run time) x V / docs.
cd /data/local/tmp/eg2; export LD_LIBRARY_PATH=/data/local/tmp/eg2
IDLE=${IDLE:-180}
cc() { cat /sys/class/power_supply/battery/charge_counter; }
stamp() { b=$(dumpsys battery); echo "$1 t=$(date +%s) cc=$(cc) mv=$(echo "$b" | grep ' voltage:' | sed 's/.*: //') level=$(echo "$b" | grep ' level' | sed 's/.*: //') usb=$(echo "$b" | grep 'USB powered' | sed 's/.*: //') temp=$(echo "$b" | grep temperature | sed 's/.*: //')"; }
input keyevent 223; sleep 30
stamp idle-start; sleep $IDLE; stamp idle-end
while read name be flags len it; do
  [ -z "$name" ] && continue
  sleep 30; stamp $name-start
  out=$(./embedding_litert_lm_main --model_path=model.litertlm --backend=$be $flags --use_mmap=true --max_input_length=$len --benchmark=true --benchmark_prefill_tokens=$len --num_iterations=$it --num_warmup=2 --input_prompt_file=p0.txt 2>&1)
  stamp $name-end
  echo "$name len=$len iters=$it $(echo "$out" | grep 'Average Latency')"
done <<CFG
cpu4-512 cpu --num_cpu_threads=4 512 160
gpu-512 gpu --activation_data_type=float16 512 270
cpu4-256 cpu --num_cpu_threads=4 256 460
CFG
stamp idle2-start; sleep 120; stamp idle2-end
echo ENERGY DONE
