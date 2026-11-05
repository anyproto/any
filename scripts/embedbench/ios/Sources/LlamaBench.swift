import Foundation
import llama

// llama.cpp half: mirrors internal/indexer/embed_local.go — n_ctx 2048,
// n_batch = n_ubatch = n_ctx, one sequence per decode, embeddings on,
// pooling mean for EG2 / model default for Qwen3, op_offload off on CPU,
// tokenize(add_special, parse_special), truncate keeping EOS last,
// memory cleared before each decode, L2-normalized output.

func runLlama(_ o: Opts, outDir: URL, log: Log) throws -> [String: Any] {
    let cj = try JSONSerialization.jsonObject(with: Data(contentsOf: documents().appendingPathComponent("payload/corpus.json")))
    guard let corpus = cj as? [String: [String]], let allDocs = corpus["docs"], let queries = corpus["queries"] else {
        throw BenchError("bad corpus.json")
    }
    let uniq = o.maxDocs > 0 ? Array(allDocs.prefix(o.maxDocs)) : allDocs
    let eg2 = o.kind == "eg2"
    let docPrefix = eg2 ? "title: none | text: " : ""
    let qPrefix = eg2 ? "task: search result | query: "
        : "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:"
    let path = documents().appendingPathComponent(o.model).path

    var memTrace: [[String: Any]] = [["stage": "start", "mem": memStats()]]
    llama_backend_init()
    let t0 = now()
    var mp = llama_model_default_params()
    if !o.gpu { mp.n_gpu_layers = 0 }
    guard let model = llama_model_load_from_file(path, mp) else { throw BenchError("load \(path) failed") }
    let threads = o.threads > 0 ? o.threads : max(1, ProcessInfo.processInfo.activeProcessorCount - 1)
    var cp = llama_context_default_params()
    cp.n_ctx = UInt32(o.ctx)
    cp.n_batch = UInt32(o.ctx)
    cp.n_ubatch = UInt32(o.ctx)
    cp.n_seq_max = 1
    cp.n_threads = Int32(threads)
    cp.n_threads_batch = Int32(threads)
    cp.pooling_type = eg2 ? LLAMA_POOLING_TYPE_MEAN : LLAMA_POOLING_TYPE_UNSPECIFIED
    cp.embeddings = true
    cp.op_offload = o.gpu
    guard let ctx = llama_init_from_model(model, cp) else { throw BenchError("init context failed") }
    let load = now() - t0
    let footAfterLoad = mb(footprint().cur)
    memTrace.append(["stage": "load", "mem": memStats()])
    let vocab = llama_model_get_vocab(model)
    let eos = llama_vocab_eos(vocab)
    var nOut = Int(llama_model_n_embd_out(model))
    if nOut <= 0 { nOut = Int(llama_model_n_embd(model)) }
    log("llama loaded in \(String(format: "%.1f", load)) s, threads \(threads), n_embd_out \(nOut), gpu \(o.gpu)")

    var truncated: [Int] = []
    func tokenize(_ text: String, idx: Int? = nil) -> [llama_token] {
        let c = Array(text.utf8CString)
        let len = Int32(c.count - 1)
        var toks = [llama_token](repeating: 0, count: Int(len) + 8)
        var n = llama_tokenize(vocab, c, len, &toks, Int32(toks.count), true, true)
        if n < 0 {
            toks = [llama_token](repeating: 0, count: Int(-n))
            n = llama_tokenize(vocab, c, len, &toks, Int32(toks.count), true, true)
        }
        var t = Array(toks.prefix(Int(max(n, 0))))
        if t.isEmpty { t = [eos] }
        if t.count > o.ctx {
            if let i = idx { truncated.append(i) }
            t = Array(t[0..<(o.ctx - 1)]) + [eos]
        }
        return t
    }

    var batch = llama_batch_init(Int32(o.ctx), 0, 1)
    defer { llama_batch_free(batch) }
    func embed(_ toks: [llama_token]) throws -> [Float] {
        for (i, t) in toks.enumerated() {
            batch.token[i] = t
            batch.pos[i] = llama_pos(i)
            batch.n_seq_id[i] = 1
            batch.seq_id[i]![0] = 0
            batch.logits[i] = 1
        }
        batch.n_tokens = Int32(toks.count)
        llama_memory_clear(llama_get_memory(ctx), true)
        let r = llama_decode(ctx, batch)
        if r != 0 { throw BenchError("decode returned \(r)") }
        guard let e = llama_get_embeddings_seq(ctx, 0) else { throw BenchError("no embeddings") }
        var v = Array(UnsafeBufferPointer(start: e, count: nOut))
        var s = 0.0
        for x in v { s += Double(x) * Double(x) }
        if s > 0 { let inv = Float(1 / s.squareRoot()); for i in v.indices { v[i] *= inv } }
        return v
    }

    _ = try embed(tokenize(docPrefix + uniq[0]))  // warm-up, as TestEmbedBench
    let footAfterWarm = mb(footprint().cur)
    memTrace.append(["stage": "warm", "mem": memStats()])
    var maxTok = 0

    var docs: [[Float]] = []
    var lat: [Double] = [], ntoks: [Double] = []
    var passWall: [Double] = []
    let start = now()
    for r in 0..<o.repeats {
        let ps = now()
        for (i, text) in uniq.enumerated() {
            let t1 = now()
            let toks = tokenize(docPrefix + text, idx: r == 0 ? i : nil)
            let v = try embed(toks)
            lat.append((now() - t1) * 1000)
            if r == 0 { docs.append(v); ntoks.append(Double(toks.count)) }
            if toks.count > maxTok {
                maxTok = toks.count
                memTrace.append(["stage": "doc \(i + 1) newMaxTok \(maxTok)", "mem": memStats()])
            }
            if r == 0 && (i + 1) % 300 == 0 { log("doc \(i + 1)/\(uniq.count) \(String(format: "%.1f", Double(i + 1) / (now() - ps))) docs/s") }
        }
        passWall.append(now() - ps)
        if o.repeats > 1 { log("pass \(r + 1)/\(o.repeats) \(String(format: "%.1f", Double(uniq.count) / passWall.last!)) docs/s") }
    }
    let wall = now() - start
    let total = uniq.count * o.repeats

    var qlat: [Double] = [], qv: [[Float]] = []
    if !o.skipQueries {
        for r in 0..<3 {
            for q in queries {
                let t1 = now()
                let v = try embed(tokenize(qPrefix + q))
                qlat.append((now() - t1) * 1000)
                if r == 0 { qv.append(v) }
            }
        }
    }

    let footEnd = mb(footprint().cur), availEnd = Double(os_proc_available_memory()) / 1_048_576
    llama_free(ctx)
    llama_model_free(model)

    let nan = (docs + qv).filter(hasNaN).count
    try writeVecs(outDir.appendingPathComponent(o.out + ".docs.f32"), docs)
    if !qv.isEmpty { try writeVecs(outDir.appendingPathComponent(o.out + ".queries.f32"), qv) }
    try truncated.map(String.init).joined(separator: "\n").write(to: outDir.appendingPathComponent(o.out + ".truncated"), atomically: true, encoding: .utf8)
    var res: [String: Any] = [
        "engine": "llama.cpp b11454", "model": o.model, "kind": o.kind, "gpu": o.gpu, "threads": threads,
        "ctx": o.ctx, "loadSec": load, "docs": uniq.count, "repeats": o.repeats, "dim": nOut,
        "docWallSec": wall, "docsPerSec": Double(total) / wall, "passDocsPerSec": passWall.map { Double(uniq.count) / $0 },
        "docLatMs": pct(lat), "tokensPerDoc": pct(ntoks), "truncatedDocs": truncated.count,
        "footprintAfterLoadMB": footAfterLoad, "footprintAfterWarmMB": footAfterWarm,
        "footprintEndMB": footEnd, "availMemEndMB": availEnd, "vectorsWithNaN": nan,
        "memTrace": memTrace + [["stage": "end", "mem": memStats()]],
    ]
    if !qlat.isEmpty { res["queryLatMs"] = pct(qlat) }
    return res
}
