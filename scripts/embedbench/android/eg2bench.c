// eg2bench: the TestEmbedBench loop (internal/indexer/embed_bench_test.go) for LiteRT-LM on Android.
// One engine, one doc per call, production prompts already applied by prep (prep_corpus.py):
//   eg2bench <model.litertlm> <cpu|gpu> <threads|0> <docs.bin> <queries.bin> <out prefix> [max_input_length] [repeat]
// docs.bin / queries.bin: NUL-separated UTF-8 texts. Writes <out>.docs.f32 and <out>.queries.f32 in the
// TestEmbedBench dump format (uint32 count, uint32 dim, float32 rows) and prints a RESULT JSON line.
// repeat > 1 re-runs the doc loop (for power windows); vectors come from the first pass.
// Build: see build.sh (NDK clang; liblitert-lm.so from litert_lm_c_api-1.0.0.zip is loaded at run time).
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <dlfcn.h>
#include "embedding_engine.h"
// liblitert-lm.so has no DT_SONAME, so bionic cannot resolve its versioned symbols at link time;
// every C API call goes through a pointer resolved with dlsym instead.
static __typeof__(litert_lm_input_data_create) *p_litert_lm_input_data_create;
static __typeof__(litert_lm_input_data_delete) *p_litert_lm_input_data_delete;
static __typeof__(litert_lm_embedding_engine_compute_embedding) *p_litert_lm_embedding_engine_compute_embedding;
static __typeof__(litert_lm_embedding_response_get_size) *p_litert_lm_embedding_response_get_size;
static __typeof__(litert_lm_embedding_response_get_values) *p_litert_lm_embedding_response_get_values;
static __typeof__(litert_lm_embedding_response_delete) *p_litert_lm_embedding_response_delete;
static __typeof__(litert_lm_set_min_log_level) *p_litert_lm_set_min_log_level;
static __typeof__(litert_lm_embedding_engine_settings_create) *p_litert_lm_embedding_engine_settings_create;
static __typeof__(litert_lm_embedding_engine_settings_set_num_threads) *p_litert_lm_embedding_engine_settings_set_num_threads;
static __typeof__(litert_lm_embedding_engine_settings_set_cache_dir) *p_litert_lm_embedding_engine_settings_set_cache_dir;
static __typeof__(litert_lm_embedding_engine_settings_set_max_input_length) *p_litert_lm_embedding_engine_settings_set_max_input_length;
static __typeof__(litert_lm_embedding_engine_create) *p_litert_lm_embedding_engine_create;
static __typeof__(litert_lm_embedding_options_create) *p_litert_lm_embedding_options_create;
static __typeof__(litert_lm_embedding_options_set_normalize) *p_litert_lm_embedding_options_set_normalize;
static __typeof__(litert_lm_embedding_options_set_input_overflow_strategy) *p_litert_lm_embedding_options_set_input_overflow_strategy;
static __typeof__(litert_lm_embedding_options_delete) *p_litert_lm_embedding_options_delete;
static __typeof__(litert_lm_embedding_engine_delete) *p_litert_lm_embedding_engine_delete;
static __typeof__(litert_lm_embedding_engine_settings_delete) *p_litert_lm_embedding_engine_settings_delete;
static void load_api(void) {
  void *h = dlopen("liblitert-lm.so", RTLD_NOW | RTLD_GLOBAL);
  if (!h) { fprintf(stderr, "dlopen: %s\n", dlerror()); exit(3); }
  if (!(p_litert_lm_input_data_create = dlsym(h, "litert_lm_input_data_create"))) { fprintf(stderr, "dlsym litert_lm_input_data_create\n"); exit(3); }
  if (!(p_litert_lm_input_data_delete = dlsym(h, "litert_lm_input_data_delete"))) { fprintf(stderr, "dlsym litert_lm_input_data_delete\n"); exit(3); }
  if (!(p_litert_lm_embedding_engine_compute_embedding = dlsym(h, "litert_lm_embedding_engine_compute_embedding"))) { fprintf(stderr, "dlsym litert_lm_embedding_engine_compute_embedding\n"); exit(3); }
  if (!(p_litert_lm_embedding_response_get_size = dlsym(h, "litert_lm_embedding_response_get_size"))) { fprintf(stderr, "dlsym litert_lm_embedding_response_get_size\n"); exit(3); }
  if (!(p_litert_lm_embedding_response_get_values = dlsym(h, "litert_lm_embedding_response_get_values"))) { fprintf(stderr, "dlsym litert_lm_embedding_response_get_values\n"); exit(3); }
  if (!(p_litert_lm_embedding_response_delete = dlsym(h, "litert_lm_embedding_response_delete"))) { fprintf(stderr, "dlsym litert_lm_embedding_response_delete\n"); exit(3); }
  if (!(p_litert_lm_set_min_log_level = dlsym(h, "litert_lm_set_min_log_level"))) { fprintf(stderr, "dlsym litert_lm_set_min_log_level\n"); exit(3); }
  if (!(p_litert_lm_embedding_engine_settings_create = dlsym(h, "litert_lm_embedding_engine_settings_create"))) { fprintf(stderr, "dlsym litert_lm_embedding_engine_settings_create\n"); exit(3); }
  if (!(p_litert_lm_embedding_engine_settings_set_num_threads = dlsym(h, "litert_lm_embedding_engine_settings_set_num_threads"))) { fprintf(stderr, "dlsym litert_lm_embedding_engine_settings_set_num_threads\n"); exit(3); }
  if (!(p_litert_lm_embedding_engine_settings_set_cache_dir = dlsym(h, "litert_lm_embedding_engine_settings_set_cache_dir"))) { fprintf(stderr, "dlsym litert_lm_embedding_engine_settings_set_cache_dir\n"); exit(3); }
  if (!(p_litert_lm_embedding_engine_settings_set_max_input_length = dlsym(h, "litert_lm_embedding_engine_settings_set_max_input_length"))) { fprintf(stderr, "dlsym litert_lm_embedding_engine_settings_set_max_input_length\n"); exit(3); }
  if (!(p_litert_lm_embedding_engine_create = dlsym(h, "litert_lm_embedding_engine_create"))) { fprintf(stderr, "dlsym litert_lm_embedding_engine_create\n"); exit(3); }
  if (!(p_litert_lm_embedding_options_create = dlsym(h, "litert_lm_embedding_options_create"))) { fprintf(stderr, "dlsym litert_lm_embedding_options_create\n"); exit(3); }
  if (!(p_litert_lm_embedding_options_set_normalize = dlsym(h, "litert_lm_embedding_options_set_normalize"))) { fprintf(stderr, "dlsym litert_lm_embedding_options_set_normalize\n"); exit(3); }
  if (!(p_litert_lm_embedding_options_set_input_overflow_strategy = dlsym(h, "litert_lm_embedding_options_set_input_overflow_strategy"))) { fprintf(stderr, "dlsym litert_lm_embedding_options_set_input_overflow_strategy\n"); exit(3); }
  if (!(p_litert_lm_embedding_options_delete = dlsym(h, "litert_lm_embedding_options_delete"))) { fprintf(stderr, "dlsym litert_lm_embedding_options_delete\n"); exit(3); }
  if (!(p_litert_lm_embedding_engine_delete = dlsym(h, "litert_lm_embedding_engine_delete"))) { fprintf(stderr, "dlsym litert_lm_embedding_engine_delete\n"); exit(3); }
  if (!(p_litert_lm_embedding_engine_settings_delete = dlsym(h, "litert_lm_embedding_engine_settings_delete"))) { fprintf(stderr, "dlsym litert_lm_embedding_engine_settings_delete\n"); exit(3); }
}

static double now_ms(void) {
  struct timespec t; clock_gettime(CLOCK_MONOTONIC, &t); return t.tv_sec * 1e3 + t.tv_nsec / 1e6;
}
static char *slurp(const char *p, size_t *n) {
  FILE *f = fopen(p, "rb"); if (!f) { perror(p); exit(1); }
  fseek(f, 0, SEEK_END); *n = ftell(f); fseek(f, 0, SEEK_SET);
  char *b = malloc(*n + 1); if (fread(b, 1, *n, f) != *n) { perror("read"); exit(1); } b[*n] = 0; fclose(f); return b;
}
static int split(char *b, size_t n, char ***out) {  // NUL-separated texts
  int c = 0; for (size_t i = 0; i < n; i++) if (b[i] == 0) c++;
  if (n && b[n - 1] != 0) c++;
  *out = malloc(sizeof(char *) * c); int k = 0; char *s = b;
  for (size_t i = 0; i <= n && k < c; i++) if (i == n || b[i] == 0) { (*out)[k++] = s; s = b + i + 1; }
  return c;
}
static int cmpd(const void *a, const void *b) { double x = *(double *)a, y = *(double *)b; return (x > y) - (x < y); }
static void pct(const char *name, double *v, int n) {
  double *s = malloc(sizeof(double) * n); memcpy(s, v, sizeof(double) * n); qsort(s, n, sizeof(double), cmpd);
  double m = 0; for (int i = 0; i < n; i++) m += s[i];
  #define P(q) s[(int)((q) * (n - 1) + 0.5)]
  printf("\"%s\":{\"mean\":%.3f,\"p50\":%.3f,\"p90\":%.3f,\"p95\":%.3f,\"p99\":%.3f,\"min\":%.3f,\"max\":%.3f}",
         name, m / n, P(.5), P(.9), P(.95), P(.99), s[0], s[n - 1]);
  free(s);
}
static long hwm_kb(void) {  // VmHWM from /proc/self/status
  FILE *f = fopen("/proc/self/status", "r"); char l[256]; long v = -1;
  while (f && fgets(l, sizeof l, f)) if (!strncmp(l, "VmHWM:", 6)) v = atol(l + 6);
  if (f) fclose(f); return v;
}
#define CHECK(x) do { LiteRtLmStatusCode _s = (x); if (_s != kLiteRtLmStatusOk) { fprintf(stderr, "FAILED %s: %d\n", #x, (int)_s); exit(2); } } while (0)

static const float *embed(LiteRtLmEmbeddingEngine *e, const LiteRtLmEmbeddingOptions *o, const char *t,
                          LiteRtLmEmbeddingResponse **r, size_t *dim) {
  LiteRtLmInputData *in; CHECK(p_litert_lm_input_data_create(kLiteRtLmInputDataTypeText, t, strlen(t), &in));
  const LiteRtLmInputData *ins[1] = {in};
  CHECK(p_litert_lm_embedding_engine_compute_embedding(e, ins, 1, o, r));
  p_litert_lm_input_data_delete(in);
  const float *v; CHECK(p_litert_lm_embedding_response_get_size(*r, dim)); CHECK(p_litert_lm_embedding_response_get_values(*r, &v));
  return v;
}
static void wvec(const char *p, float *m, unsigned n, unsigned d) {
  FILE *f = fopen(p, "wb"); fwrite(&n, 4, 1, f); fwrite(&d, 4, 1, f); fwrite(m, 4, (size_t)n * d, f); fclose(f);
}

int main(int argc, char **argv) {
  if (argc < 7) { fprintf(stderr, "usage: %s model backend threads docs.bin queries.bin out [max_len] [repeat]\n", argv[0]); return 1; }
  const char *model = argv[1], *backend = argv[2], *out = argv[6];
  int threads = atoi(argv[3]), maxlen = argc > 7 ? atoi(argv[7]) : 2048, repeat = argc > 8 ? atoi(argv[8]) : 1;
  size_t nd, nq; char *db = slurp(argv[4], &nd), *qb = slurp(argv[5], &nq); char **docs, **qs;
  int ndocs = split(db, nd, &docs), nqs = split(qb, nq, &qs);
  load_api();
  p_litert_lm_set_min_log_level(kLiteRtLmLogSeverityError);

  double t0 = now_ms();
  LiteRtLmEmbeddingEngineSettings *st;
  CHECK(p_litert_lm_embedding_engine_settings_create(model, backend, NULL, NULL, &st));
  if (threads > 0) CHECK(p_litert_lm_embedding_engine_settings_set_num_threads(st, threads));
  CHECK(p_litert_lm_embedding_engine_settings_set_cache_dir(st, "/data/local/tmp/eg2"));
  CHECK(p_litert_lm_embedding_engine_settings_set_max_input_length(st, maxlen));
  LiteRtLmEmbeddingEngine *e; CHECK(p_litert_lm_embedding_engine_create(st, &e));
  LiteRtLmEmbeddingOptions *o; CHECK(p_litert_lm_embedding_options_create(&o));
  CHECK(p_litert_lm_embedding_options_set_normalize(o, true));
  CHECK(p_litert_lm_embedding_options_set_input_overflow_strategy(o, kLiteRtLmInputOverflowStrategyTruncate));
  double load = now_ms() - t0;

  LiteRtLmEmbeddingResponse *r; size_t dim = 0;
  embed(e, o, docs[0], &r, &dim); p_litert_lm_embedding_response_delete(r);              // warm-up: one doc,
  embed(e, o, qs[0], &r, &dim); p_litert_lm_embedding_response_delete(r);                // one query

  float *dv = malloc(sizeof(float) * (size_t)ndocs * dim); double *lat = malloc(sizeof(double) * ndocs);
  long chars = 0; for (int i = 0; i < ndocs; i++) chars += strlen(docs[i]);
  double wall0 = now_ms(), pass1 = 0;
  for (int pass = 0; pass < repeat; pass++) {
    for (int i = 0; i < ndocs; i++) {
      double a = now_ms(); const float *v = embed(e, o, docs[i], &r, &dim); double b = now_ms();
      if (pass == 0) { lat[i] = b - a; memcpy(dv + (size_t)i * dim, v, sizeof(float) * dim); }
      p_litert_lm_embedding_response_delete(r);
    }
    if (pass == 0) pass1 = now_ms() - wall0;
    fprintf(stderr, "pass %d done at %.1f s\n", pass + 1, (now_ms() - wall0) / 1e3);
  }
  double wall = now_ms() - wall0;
  double *ql = malloc(sizeof(double) * nqs * 3); float *qv = malloc(sizeof(float) * (size_t)nqs * dim);
  for (int round = 0; round < 3; round++)
    for (int i = 0; i < nqs; i++) {
      double a = now_ms(); const float *v = embed(e, o, qs[i], &r, &dim); ql[round * nqs + i] = now_ms() - a;
      if (round == 0) memcpy(qv + (size_t)i * dim, v, sizeof(float) * dim);
      p_litert_lm_embedding_response_delete(r);
    }
  char p[1024];
  snprintf(p, sizeof p, "%s.docs.f32", out); wvec(p, dv, ndocs, dim);
  snprintf(p, sizeof p, "%s.queries.f32", out); wvec(p, qv, nqs, dim);
  printf("RESULT {\"backend\":\"%s\",\"threads\":%d,\"maxInputLength\":%d,\"docs\":%d,\"chars\":%ld,\"dim\":%zu,"
         "\"loadSec\":%.3f,\"docWallSec\":%.3f,\"docsPerSec\":%.3f,\"kCharsPerSec\":%.3f,\"repeat\":%d,\"allPassesWallSec\":%.3f,",
         backend, threads, maxlen, ndocs, chars, dim, load / 1e3, pass1 / 1e3, ndocs / (pass1 / 1e3), chars / pass1, repeat, wall / 1e3);
  pct("docLatMs", lat, ndocs); printf(","); pct("queryLatMs", ql, nqs * 3);
  printf(",\"peakRSSMB\":%.1f}\n", hwm_kb() / 1024.0);
  p_litert_lm_embedding_options_delete(o); p_litert_lm_embedding_engine_delete(e); p_litert_lm_embedding_engine_settings_delete(st);
  return 0;
}
