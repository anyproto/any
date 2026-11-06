import Foundation
import UIKit

typealias Log = (String) -> Void

struct BenchError: Error, CustomStringConvertible {
    let description: String
    init(_ s: String) { description = s }
}

struct Opts {
    var mode = "coreml"        // coreml | plan | llama | idle
    var model = ""             // path under Documents/
    var units = "ane"          // ane | cpu | gpu | all
    var buckets: [Int] = [512]
    var fnFmt = ""             // multifunction name, "s%d"
    var mask = ""              // attention-mask input name
    var maxDocs = 0
    var batch = 8
    var skipQueries = false
    var out = "run"
    var repeats = 1            // corpus passes (power runs)
    var kind = "eg2"           // llama: eg2 | qwen3 | granite
    var payload = "payload"    // coreml: dir under Documents/ holding tokens.bin
    var gpu = true             // llama: Metal on/off
    var threads = 0            // llama: 0 = NumCPU-1, as the Go embedder
    var ctx = 2048
    var idleSec = 0            // idle mode duration

    init(_ args: [String]) throws {
        var i = 0
        func val() throws -> String {
            i += 1
            guard i < args.count else { throw BenchError("missing value for \(args[i - 1])") }
            return args[i]
        }
        while i < args.count {
            switch args[i] {
            case "--mode": mode = try val()
            case "--model": model = try val()
            case "--units": units = try val()
            case "--buckets": buckets = try val().split(separator: ",").compactMap { Int($0) }.sorted()
            case "--function-fmt": fnFmt = try val()
            case "--mask-input": mask = try val()
            case "--max-docs": maxDocs = Int(try val()) ?? 0
            case "--batch": batch = Int(try val()) ?? 1
            case "--skip-queries": skipQueries = true
            case "--out": out = try val()
            case "--repeat": repeats = Int(try val()) ?? 1
            case "--kind": kind = try val()
            case "--payload": payload = try val()
            case "--cpu": gpu = false
            case "--threads": threads = Int(try val()) ?? 0
            case "--ctx": ctx = Int(try val()) ?? 2048
            case "--idle-sec": idleSec = Int(try val()) ?? 60
            default: throw BenchError("unknown arg \(args[i])")
            }
            i += 1
        }
    }
}

func documents() -> URL {
    FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
}

func now() -> Double { CFAbsoluteTimeGetCurrent() }

func pct(_ xs: [Double]) -> [String: Double] {
    guard !xs.isEmpty else { return [:] }
    let s = xs.sorted()
    func at(_ p: Double) -> Double { s[min(s.count - 1, Int(p * Double(s.count)))] }
    return ["min": s[0], "p50": at(0.5), "p90": at(0.9), "p95": at(0.95), "p99": at(0.99),
            "max": s[s.count - 1], "mean": s.reduce(0, +) / Double(s.count)]
}

func footprint() -> (cur: UInt64, peak: UInt64) {
    var info = task_vm_info_data_t()
    var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<integer_t>.size)
    let kr = withUnsafeMutablePointer(to: &info) {
        $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
            task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
        }
    }
    guard kr == KERN_SUCCESS else { return (0, 0) }
    return (info.phys_footprint, UInt64(max(0, info.ledger_phys_footprint_peak)))
}

func mb(_ b: UInt64) -> Double { Double(b) / 1_048_576 }

/// Memory ledgers of this task, MB. neural* = memory the kernel tags as
/// Neural Engine, graphics* = GPU/IOSurface; "noFoot" = tagged but not
/// charged to phys_footprint.
func memStats() -> [String: Double] {
    var info = task_vm_info_data_t()
    var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<integer_t>.size)
    let kr = withUnsafeMutablePointer(to: &info) {
        $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
            task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
        }
    }
    guard kr == KERN_SUCCESS else { return [:] }
    func m(_ x: Int64) -> Double { Double(max(0, x)) / 1_048_576 }
    return ["footprint": mb(info.phys_footprint), "peak": m(info.ledger_phys_footprint_peak),
            "neural": m(info.ledger_tag_neural_footprint), "neuralNoFoot": m(info.ledger_tag_neural_nofootprint),
            "neuralNoFootTotal": m(info.ledger_tag_neural_nofootprint_total),
            "graphics": m(info.ledger_tag_graphics_footprint), "graphicsNoFoot": m(info.ledger_tag_graphics_nofootprint),
            "internal": mb(info.internal), "compressed": mb(info.compressed),
            "availMB": Double(os_proc_available_memory()) / 1_048_576]
}

/// Samples footprint and thermal state every 250 ms while a run is live.
final class Sampler {
    private let lock = NSLock()
    private var maxFoot: UInt64 = 0
    private var maxThermal = 0
    private var thermalChanges: [[String: Any]] = []
    private var running = true
    private let t0 = now()

    init() {
        Thread.detachNewThread { [self] in
            var last = -1
            while true {
                lock.lock()
                let go = running
                lock.unlock()
                if !go { break }
                let f = footprint().cur
                let th = ProcessInfo.processInfo.thermalState.rawValue
                lock.lock()
                maxFoot = max(maxFoot, f)
                maxThermal = max(maxThermal, th)
                if th != last {
                    thermalChanges.append(["t": (now() - t0).rounded(), "state": th])
                    last = th
                }
                lock.unlock()
                Thread.sleep(forTimeInterval: 0.25)
            }
        }
    }

    func stop() -> [String: Any] {
        lock.lock()
        running = false
        let r: [String: Any] = ["maxFootprintMB": mb(maxFoot), "maxThermalState": maxThermal,
                                "thermalChanges": thermalChanges]
        lock.unlock()
        return r
    }
}

func deviceInfo() -> [String: Any] {
    var u = utsname()
    uname(&u)
    let machine = withUnsafeBytes(of: &u.machine) { String(cString: $0.bindMemory(to: CChar.self).baseAddress!) }
    let d = UIDevice.current
    return ["machine": machine, "os": d.systemVersion, "lowPowerMode": ProcessInfo.processInfo.isLowPowerModeEnabled,
            "thermalState": ProcessInfo.processInfo.thermalState.rawValue,
            "battery": d.batteryLevel, "batteryState": d.batteryState.rawValue,
            "cpus": ProcessInfo.processInfo.activeProcessorCount,
            "ramGB": Double(ProcessInfo.processInfo.physicalMemory) / 1_073_741_824,
            "availMemMB": Double(os_proc_available_memory()) / 1_048_576]
}

func writeVecs(_ url: URL, _ vecs: [[Float]]) throws {
    guard let dim = vecs.first?.count else { return }
    var d = Data()
    var n = UInt32(vecs.count).littleEndian, m = UInt32(dim).littleEndian
    d.append(Data(bytes: &n, count: 4))
    d.append(Data(bytes: &m, count: 4))
    for v in vecs { v.withUnsafeBufferPointer { d.append(Data(buffer: $0)) } }
    try d.write(to: url)
}

func cosine(_ a: [Float], _ b: [Float]) -> Double {
    var ab = 0.0, aa = 0.0, bb = 0.0
    for i in 0..<min(a.count, b.count) {
        ab += Double(a[i]) * Double(b[i]); aa += Double(a[i]) * Double(a[i]); bb += Double(b[i]) * Double(b[i])
    }
    return ab / (aa.squareRoot() * bb.squareRoot())
}

func hasNaN(_ v: [Float]) -> Bool { v.contains { !$0.isFinite } }

func readTokens(_ url: URL) throws -> (docs: [[Int32]], queries: [[Int32]]) {
    let d = try Data(contentsOf: url)
    return try d.withUnsafeBytes { raw -> ([[Int32]], [[Int32]]) in
        guard raw.count > 12, String(bytes: raw[0..<4], encoding: .ascii) == "TOK1" else { throw BenchError("bad tokens.bin") }
        var off = 4
        func u32() -> Int {
            let v = raw.loadUnaligned(fromByteOffset: off, as: UInt32.self)
            off += 4
            return Int(UInt32(littleEndian: v))
        }
        let nd = u32(), nq = u32()
        var all: [[Int32]] = []
        all.reserveCapacity(nd + nq)
        for _ in 0..<(nd + nq) {
            let n = u32()
            var ids = [Int32](repeating: 0, count: n)
            for j in 0..<n { ids[j] = Int32(littleEndian: raw.loadUnaligned(fromByteOffset: off + 4 * j, as: Int32.self)) }
            off += 4 * n
            all.append(ids)
        }
        return (Array(all[0..<nd]), Array(all[nd...]))
    }
}

func benchMain(args: [String], log: @escaping Log) -> Int32 {
    do {
        let o = try Opts(args)
        let outDir = documents().appendingPathComponent("out")
        try FileManager.default.createDirectory(at: outDir, withIntermediateDirectories: true)
        log("START \(o.mode) \(o.out) t=\(Int(Date().timeIntervalSince1970)) \(deviceInfo())")
        let sampler = Sampler()
        let t0 = now()
        var res: [String: Any]
        switch o.mode {
        case "coreml": res = try runCoreML(o, outDir: outDir, log: log)
        case "plan": res = try runPlan(o, log: log)
        case "llama": res = try runLlama(o, outDir: outDir, log: log)
        case "idle":
            Thread.sleep(forTimeInterval: Double(o.idleSec))
            res = ["idleSec": o.idleSec]
        default: throw BenchError("unknown mode \(o.mode)")
        }
        res["sampler"] = sampler.stop()
        res["device"] = deviceInfo()
        res["totalSec"] = now() - t0
        res["startEpoch"] = Date().timeIntervalSince1970 - (now() - t0)
        res["endEpoch"] = Date().timeIntervalSince1970
        res["peakFootprintMB"] = mb(footprint().peak)
        let data = try JSONSerialization.data(withJSONObject: res, options: [.prettyPrinted, .sortedKeys])
        try data.write(to: outDir.appendingPathComponent(o.out + ".json"))
        log("RESULT " + (String(data: data, encoding: .utf8) ?? "{}"))
        log("DONE \(o.out) t=\(Int(Date().timeIntervalSince1970))")
        return 0
    } catch {
        log("ERROR \(error)")
        return 1
    }
}
