import CoreML
import Foundation

// Core ML half: the same loop as coreml_bench.py on the Mac. The corpus
// comes pre-tokenized (tokens.bin, HF tokenizer.json with BOS/EOS), so the
// timed latency is array fill + predict + output copy; the Mac's
// docPredictLatMs is the comparable number.

func computeUnits(_ s: String) -> MLComputeUnits {
    switch s {
    case "cpu": return .cpuOnly
    case "gpu": return .cpuAndGPU
    case "all": return .all
    default: return .cpuAndNeuralEngine
    }
}

func embedding(_ out: MLFeatureProvider, _ name: String = "embedding") throws -> [Float] {
    guard let a = out.featureValue(for: name)?.multiArrayValue else { throw BenchError("no \(name) output") }
    let n = a.count
    var v = [Float](repeating: 0, count: n)
    switch a.dataType {
    case .float32: a.withUnsafeBufferPointer(ofType: Float.self) { p in for i in 0..<n { v[i] = p[i] } }
    case .float16: a.withUnsafeBufferPointer(ofType: Float16.self) { p in for i in 0..<n { v[i] = Float(p[i]) } }
    default: for i in 0..<n { v[i] = a[i].floatValue }
    }
    return v
}

func runCoreML(_ o: Opts, outDir: URL, log: Log) throws -> [String: Any] {
    let (allDocs, qToks) = try readTokens(documents().appendingPathComponent(o.payload + "/tokens.bin"))
    let uniq = o.maxDocs > 0 ? Array(allDocs.prefix(o.maxDocs)) : allDocs
    let buckets = o.buckets
    guard let maxLen = buckets.last else { throw BenchError("no buckets") }
    let eos: Int32 = 1, pad: Int32 = 0
    let url = documents().appendingPathComponent(o.model)

    // Load: one MLModel per function. First-ever load compiles for the ANE.
    var models: [Int: MLModel] = [:]
    var loadPer: [String: Double] = [:]
    var memTrace: [[String: Any]] = [["stage": "start", "mem": memStats()]]
    let t0 = now()
    for L in buckets {
        let cfg = MLModelConfiguration()
        cfg.computeUnits = computeUnits(o.units)
        if !o.fnFmt.isEmpty { cfg.functionName = o.fnFmt.replacingOccurrences(of: "%d", with: "\(L)") }
        let t = now()
        models[L] = try MLModel(contentsOf: url, configuration: cfg)
        loadPer["\(L)"] = now() - t
        memTrace.append(["stage": "load \(L)", "mem": memStats()])
        log("loaded \(L) in \(String(format: "%.1f", now() - t)) s")
    }
    let load = now() - t0
    let footAfterLoad = mb(footprint().cur)

    func provider(_ ids: [Int32], _ L: Int) throws -> MLFeatureProvider {
        let x = try MLMultiArray(shape: [1, NSNumber(value: L)], dataType: .int32)
        x.withUnsafeMutableBufferPointer(ofType: Int32.self) { p, _ in
            for i in 0..<L { p[i] = i < ids.count ? ids[i] : pad }
        }
        var dict: [String: MLFeatureValue] = ["input_ids": MLFeatureValue(multiArray: x)]
        if !o.mask.isEmpty {
            let m = try MLMultiArray(shape: [1, NSNumber(value: L)], dataType: .float32)
            m.withUnsafeMutableBufferPointer(ofType: Float.self) { p, _ in
                for i in 0..<L { p[i] = i < ids.count ? 1 : 0 }
            }
            dict[o.mask] = MLFeatureValue(multiArray: m)
        }
        return try MLDictionaryFeatureProvider(dictionary: dict)
    }

    var truncated: [Int] = []
    func feed(_ ids0: [Int32], idx: Int? = nil) throws -> (Int, MLFeatureProvider, Int) {
        var ids = ids0
        if ids.count > maxLen {
            if let i = idx { truncated.append(i) }
            let last = ids.last == eos ? eos : ids[maxLen - 1]
            ids = Array(ids[0..<(maxLen - 1)]) + [last]
        }
        guard let L = buckets.first(where: { $0 >= ids.count }) else { throw BenchError("no bucket") }
        return (L, try provider(ids, L), ids.count)
    }

    // Warm-up: first predict per function, 16 tokens of doc 0.
    var first: [String: Double] = [:]
    let warm = Array(uniq[0].prefix(16))
    for L in buckets {
        try autoreleasepool {
            let d = try provider(warm, L)
            let t = now()
            _ = try models[L]!.prediction(from: d)
            first["\(L)"] = now() - t
        }
        memTrace.append(["stage": "warm \(L)", "mem": memStats()])
    }
    let footAfterWarm = mb(footprint().cur)

    // One doc per predict, `repeats` passes; vectors kept from pass 1.
    var docs: [[Float]] = []
    var lat: [Double] = [], ntoks: [Double] = []
    var used: [String: Int] = [:]
    var passWall: [Double] = []
    let start = now()
    for r in 0..<o.repeats {
        let ps = now()
        for (i, ids) in uniq.enumerated() {
            try autoreleasepool {
                let t1 = now()
                let (L, d, n) = try feed(ids, idx: r == 0 ? i : nil)
                let v = try embedding(models[L]!.prediction(from: d))
                let t2 = now()
                if r == 0 {
                    docs.append(v)
                    ntoks.append(Double(n))
                    used["\(L)", default: 0] += 1
                }
                lat.append((t2 - t1) * 1000)
                if r == 0 && (i + 1) % 250 == 0 { memTrace.append(["stage": "doc \(i + 1)", "mem": memStats()]) }
            }
        }
        passWall.append(now() - ps)
        if o.repeats > 1 { log("pass \(r + 1)/\(o.repeats) \(String(format: "%.1f", Double(uniq.count) / passWall.last!)) docs/s") }
    }
    let wall = now() - start
    let total = uniq.count * o.repeats

    var qlat: [Double] = [], qv: [[Float]] = []
    if !o.skipQueries {
        for r in 0..<3 {
            for q in qToks {
                try autoreleasepool {
                    let t1 = now()
                    let (L, d, _) = try feed(q)
                    let v = try embedding(models[L]!.prediction(from: d))
                    qlat.append((now() - t1) * 1000)
                    if r == 0 { qv.append(v) }
                }
            }
        }
    }

    // Batched pass: predictions(fromBatch:) per bucket.
    var bres: [String: Any]? = nil
    if o.batch > 1 {
        var groups: [Int: [(Int, MLFeatureProvider)]] = [:]
        for (i, ids) in uniq.enumerated() {
            let (L, d, _) = try feed(ids)
            groups[L, default: []].append((i, d))
        }
        var bdocs = [[Float]](repeating: [], count: uniq.count)
        let bs = now()
        for (L, items) in groups {
            var j = 0
            while j < items.count {
                try autoreleasepool {
                    let chunk = Array(items[j..<min(j + o.batch, items.count)])
                    let outs = try models[L]!.predictions(fromBatch: MLArrayBatchProvider(array: chunk.map { $0.1 }))
                    for (k, (i, _)) in chunk.enumerated() { bdocs[i] = try embedding(outs.features(at: k)) }
                }
                j += o.batch
            }
        }
        let bwall = now() - bs
        let agree = zip(bdocs, docs).map { cosine($0, $1) }.min() ?? 0
        bres = ["batch": o.batch, "wallSec": bwall, "docsPerSec": Double(uniq.count) / bwall, "minCosVsSingle": agree]
    }

    let nan = (docs + qv).filter(hasNaN).count
    try writeVecs(outDir.appendingPathComponent(o.out + ".docs.f32"), docs)
    if !qv.isEmpty { try writeVecs(outDir.appendingPathComponent(o.out + ".queries.f32"), qv) }
    try truncated.map(String.init).joined(separator: "\n").write(to: outDir.appendingPathComponent(o.out + ".truncated"), atomically: true, encoding: .utf8)
    var res: [String: Any] = [
        "engine": "coreml", "model": o.model, "computeUnits": o.units, "buckets": buckets, "bucketUse": used,
        "loadSec": load, "loadPerFunctionSec": loadPer, "firstPredictSec": first,
        "docs": uniq.count, "repeats": o.repeats, "dim": docs.first?.count ?? 0,
        "docWallSec": wall, "docsPerSec": Double(total) / wall, "passDocsPerSec": passWall.map { Double(uniq.count) / $0 },
        "docLatMs": pct(lat), "tokensPerDoc": pct(ntoks), "truncatedDocs": truncated.count,
        "footprintAfterLoadMB": footAfterLoad, "footprintAfterWarmMB": footAfterWarm,
        "footprintEndMB": mb(footprint().cur), "availMemEndMB": Double(os_proc_available_memory()) / 1_048_576,
        "vectorsWithNaN": nan, "memTrace": memTrace + [["stage": "end", "mem": memStats()]],
    ]
    if !qlat.isEmpty { res["queryLatMs"] = pct(qlat) }
    if let b = bres { res["batched"] = b }
    return res
}

// MLComputePlan on the device: preferred device per op and cost share,
// for each requested function.
func runPlan(_ o: Opts, log: Log) throws -> [String: Any] {
    let url = documents().appendingPathComponent(o.model)
    var res: [String: Any] = [:]
    let names = o.fnFmt.isEmpty ? [""] : o.buckets.map { o.fnFmt.replacingOccurrences(of: "%d", with: "\($0)") }
    for name in names {
        let cfg = MLModelConfiguration()
        cfg.computeUnits = computeUnits(o.units)
        if !name.isEmpty { cfg.functionName = name }
        let sem = DispatchSemaphore(value: 0)
        var out: [String: Any] = [:]
        var err: Error?
        Task.detached {
            do {
                let plan = try await MLComputePlan.load(contentsOf: url, configuration: cfg)
                guard case let .program(program) = plan.modelStructure else { throw BenchError("not an ML program") }
                for (fname, fn) in program.functions where name.isEmpty || fname == name || program.functions.count == 1 {
                    var ops: [String: Int] = [:], cost: [String: Double] = [:], cpuOps: [String: Int] = [:]
                    func walk(_ block: MLModelStructure.Program.Block) {
                        for op in block.operations {
                            var dev = "none"
                            if let u = plan.deviceUsage(for: op) {
                                switch u.preferred {
                                case .cpu: dev = "cpu"
                                case .gpu: dev = "gpu"
                                case .neuralEngine: dev = "ane"
                                @unknown default: dev = "other"
                                }
                            }
                            if dev != "none" || op.blocks.isEmpty {
                                ops[dev, default: 0] += 1
                                cost[dev, default: 0] += plan.estimatedCost(of: op)?.weight ?? 0
                                if dev == "cpu" { cpuOps[op.operatorName, default: 0] += 1 }
                            }
                            for b in op.blocks { walk(b) }
                        }
                    }
                    walk(fn.block)
                    out[fname] = ["ops": ops, "cost": cost, "cpuOps": cpuOps]
                }
            } catch { err = error }
            sem.signal()
        }
        sem.wait()
        if let e = err { throw e }
        for (k, v) in out { res[k] = v; log("plan \(k): \(v)") }
    }
    return ["engine": "plan", "model": o.model, "computeUnits": o.units, "functions": res]
}
