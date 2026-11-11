#!/system/bin/sh
# Latency while charging: screen off vs screen on (timeout raised for the run, restored after).
cd /data/local/tmp/eg2; export LD_LIBRARY_PATH=/data/local/tmp/eg2
OLD=$(settings get system screen_off_timeout)
b() { ./embedding_litert_lm_main --model_path=model.litertlm --backend=$1 $2 --use_mmap=true --max_input_length=$3 --benchmark=true --benchmark_prefill_tokens=$3 --num_iterations=$4 --input_prompt_file=p0.txt 2>&1 | grep 'Average Latency'; }
input keyevent 223; sleep 20
echo "screen-off charging cpu4-256: $(b cpu --num_cpu_threads=4 256 40)"
echo "screen-off charging gpu-512: $(b gpu --activation_data_type=float16 512 30)"
settings put system screen_off_timeout 600000; input keyevent 224; sleep 5
echo "screen-on charging cpu4-256: $(b cpu --num_cpu_threads=4 256 40)"
echo "screen-on charging gpu-512: $(b gpu --activation_data_type=float16 512 30)"
settings put system screen_off_timeout $OLD
echo "restored timeout $(settings get system screen_off_timeout); CHARGING DONE"
