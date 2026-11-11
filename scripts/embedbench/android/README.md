# EG2 on Android: LiteRT int4 on a Nothing Phone (2a) (SYN-391)

Roma's phone, 2026-11-11. The question from item 6: does the EG2 head projection carry over to the LiteRT int4
build that Android would ship, and what does EG2 cost there in time, memory and energy?

## Setup

- **Phone:** Nothing Phone (2a), A142 "Pacman". MediaTek Dimensity 7200 Pro (MT6886): 2× 2.8 GHz + 6× 2.0 GHz,
  Mali-G610 GPU, no NPU build. Android 16, 5,000 mAh battery.
- **Model:** `litert-community/embeddinggemma-2-text-270m-litert-lm`, `embeddinggemma-2-text-270m.litertlm`
  (165 MB; int4 per-channel QAT; text only). It's the same text model as the ggml-org GGUF used on desktop and
  iPhone, which never loaded the separate `mmproj` vision/audio file.
- **Runtime:** LiteRT-LM v0.19.1. On the phone, `embedding_litert_lm_main` from
  `gs://litert/binaries/latest/android_arm64/litert_lm/`; on the Mac, the `litert-lm-api` wheel.
- **Prompts:** production prompts (`title: none | text: `, `task: search result | query: `), mean pooling done
  by the model, L2-normalized, 768 dims.

## 1. Quality: the projection carries over (`score.py`, `results/quality.md`)

Five public sets, 936 queries, vector-only nDCG@10. Embedded on the Mac's LiteRT GPU path for speed. The phone's
CPU gives the same vectors: cos 0.998 against the Mac's on the same doc.

| set | desktop EG2, stock | desktop, head | LiteRT int4, stock | LiteRT, head | head on LiteRT | LiteRT head − desktop head | top-10 hits < 80 chars, LiteRT (desktop) |
|---|---|---|---|---|---|---|---|
| ru (48 q) | 0.872 | 0.897 | 0.850 | 0.863 | +0.013 | −0.034* | 34% → 10% (35% → 10%) |
| miracl-ru-pool (200 q) | 0.863 | 0.859 | 0.861 | 0.862 | +0.001 | +0.003 | ~0% |
| wiki-mix (65 q) | 0.753 | 0.797 | 0.779 | 0.802 | +0.023* | +0.006 | 54% → 37% (55% → 37%) |
| scifact (300 q) | 0.853 | 0.850 | 0.835 | 0.839 | +0.004 | −0.011 | 0% |
| nfcorpus (323 q) | 0.355 | 0.357 | 0.355 | 0.355 | 0.000 | −0.003 | 0% |
| **all (936 q)** | 0.677 | 0.681 | 0.672 | 0.675 | +0.004 | **−0.005** (n.s.) | |

- **The projection transfers as is.** `head_mean.npy` from the desktop model applies unchanged to the int4
  vectors: the shared direction is there at the same strength (|u·v| 0.686 against 0.677), and it cuts the
  short-hit share exactly as on desktop.
- **int4 costs nothing measurable overall:** −0.005 (n.s.) against desktop EG2 with the head. Vectors match the
  desktop ones at cos 0.980–0.986. The one significant set gap is RU wiki (−0.034*, 48 queries); MIRACL-RU
  (200 queries) is level.

## 2. Speed and memory (`sweep.sh`, `results/sweep.log`)

Phone on the charger, benchmark mode, latency per embedding at a fixed token count (average of 8–20).

| tokens | CPU 2 threads | CPU 4 | CPU 8 | GPU fp32 | GPU fp16 | peak RAM, CPU / GPU |
|---|---|---|---|---|---|---|
| 128 | 158 ms | **142 ms** | 174 ms | 190 ms | 194 ms | 328 MB / 586 MB |
| 256 | 403 ms | 364 ms | 337 ms | 397 ms | **319 ms** | 361 MB / 1.04 GB |
| 512 | 1,153 ms | 1,044 ms | 958 ms | **615 ms** | 616 ms | 419 MB / 1.92 GB |
| 1024 | 3,725 ms | 3,251 ms | 2,989 ms | **1,204 ms** | 1,202 ms | 525 MB / 2.13 GB |

- **CPU time grows faster than linearly; GPU time grows linearly.** At production chunk sizes (about 300–1,350
  tokens), the GPU is 1.7–2.7× faster than the CPU.
- **The GPU's peak RAM is 1–2 GB, against 0.3–0.5 GB on the CPU.** That's a lot for a background indexer.
- **Init takes 0.7–0.9 s on the CPU and 2–4.5 s on the GPU,** with the caches warm.

## 3. Energy (`energy.sh`, `sampler.sh`, `power.py`, `results/energy.log`, `results/current.log`)

- **Method:** cable unplugged, adb over Wi-Fi, screen off. Battery current comes from
  `IBatteryPropertiesRegistrar.getProperty(CURRENT_NOW)` (`service call batteryproperties 1 i32 2`), voltage
  from `dumpsys battery`, sampled about every 3 s.
- **Baseline:** the idle phase at the end, 1.18 W with Wi-Fi, adb and the sampler running, subtracted as the
  baseline. The two gaps between runs read 1.12 and 1.26 W.
- **Not used:** sysfs `current_now` is closed to the shell, and `charge_counter` is only percent × capacity, in
  1% steps of about 675 J.

| config (on battery, screen off, 11–16%) | latency | gross W | net W | **net J / embedding** |
|---|---|---|---|---|
| CPU 4 threads, 256 tokens (460 runs) | 1,111 ms | 4.10 | 2.92 | **3.3** |
| CPU 4 threads, 512 tokens (160) | 3,071 ms | 4.31 | 3.13 | **9.8** |
| GPU fp16, 512 tokens (270) | 1,726 ms | 4.14 | 2.96 | **5.3** |

- **Against Apple, measured earlier on about 180-token FiQA docs:** iPhone 17 Pro EG2 on the ANE 0.084 J per doc,
  on Metal 0.19; M2 Air on the ANE 0.16. Per token, this phone's CPU (12.8 mJ) is about 12× iPhone Metal
  (1.1 mJ).
- **A 10,000-chunk index of 256-token chunks:** about 9 Wh on the CPU, half of this phone's battery (about
  19 Wh), and about 3 h with the screen off.

## 4. Throttling on battery (`charging.sh`)

Phone at 11%, Battery Saver off, battery temperature 28–30 °C, so it isn't thermal.

| | CPU 4 threads, 256 tokens | GPU fp16, 512 tokens |
|---|---|---|
| charging, screen off | **361 ms** | 896 ms |
| charging, screen on | 491 ms | 712 ms |
| sweep (charging) | 364 ms | 616 ms |
| battery, screen off | **1,111 ms** (min 352) | 1,726 ms (min 615) |

On battery with the screen off, the phone runs at full speed only now and then: the minimum matches the
sweep, the average is about 3× slower. On the charger, screen off is full speed. Not separated yet: whether this
is an on-battery policy at any level or a low-battery (≤ 15%) one.

## What it means for item 6

- **Quality:** EG2 on Android is the same model as on desktop, within noise, and the head projection carries over
  as a post-step: `v' = normalize(v − u(u·v))`, with the same `u`.
- **Cost:** on a mid-range phone, embedding is about 10× the iPhone's energy per token. Indexing should run while
  charging (for example WorkManager `requiresCharging`), where it's also 3× faster than on battery with the screen
  off, and it should stay off the GPU unless the 1–2 GB peak is acceptable.
- **Backend:** CPU with 4 threads for short chunks and low RAM; GPU only for long chunks when memory allows.
