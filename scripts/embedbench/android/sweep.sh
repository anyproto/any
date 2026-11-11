#!/system/bin/sh
cd /data/local/tmp/eg2; export LD_LIBRARY_PATH=/data/local/tmp/eg2
run() { # name backend extra len iters
  out=$(./embedding_litert_lm_main --model_path=model.litertlm --backend=$2 $3 --use_mmap=true --max_input_length=$4 --benchmark=true --benchmark_prefill_tokens=$4 --num_iterations=$5 --report_peak_memory_footprint=true --input_prompt_file=p0.txt 2>&1)
  lat=$(echo "$out" | grep 'Average Latency' | sed 's/Average Latency: //'); ram=$(echo "$out" | grep 'Peak system ram' | sed 's/.*: //'); acc=$(echo "$out" | grep 'Peak accelerator' | sed 's/.*: //'); ini=$(echo "$out" | grep 'Init Time' | sed 's/.*: //')
  [ -z "$lat" ] && lat="FAILED $(echo "$out" | grep -i -m2 'error\|fail' | tr '\n' ' ')"
  echo "$1 len=$4 | $lat | ram $ram | acc $acc | init $ini | temp $(dumpsys battery | grep temperature | sed 's/.*: //')"
  sleep 8
}
for L in 128 256 512 1024; do
  N=20; [ $L -ge 1024 ] && N=8
  run cpu2 cpu --num_cpu_threads=2 $L $N
  run cpu4 cpu --num_cpu_threads=4 $L $N
  run cpu8 cpu --num_cpu_threads=8 $L $N
  run gpu gpu "" $L $N
  run gpu-fp16 gpu --activation_data_type=float16 $L $N
done
echo SWEEP DONE
