import Foundation
import CoreML

struct TokenRow: Decodable { let text: String; let tokens: [Int32] }
struct ReferenceRow: Decodable { let `case`: Int; let text: String; let tokens: [Int32]; let length: Int; let vector: [Double] }

func write(_ value: Any, _ path: String) throws {
    try JSONSerialization.data(withJSONObject:value,options:[.prettyPrinted,.sortedKeys]).write(to:URL(fileURLWithPath:path))
}
func footprint(_ root:String, _ tag:String) throws {
    let p=Process();p.executableURL=URL(fileURLWithPath:"/usr/bin/footprint")
    p.arguments=["--wired",String(ProcessInfo.processInfo.processIdentifier)]
    let path=root+"/evidence/footprint-"+tag+".txt"
    FileManager.default.createFile(atPath:path,contents:nil)
    let h=try FileHandle(forWritingTo:URL(fileURLWithPath:path));p.standardOutput=h;p.standardError=h
    try p.run();p.waitUntilExit();try h.close()
}
func provider(_ ids:[Int32],_ length:Int) throws -> MLFeatureProvider {
    precondition(ids.count<=length)
    let x=try MLMultiArray(shape:[1,NSNumber(value:length)],dataType:.int32)
    let m=try MLMultiArray(shape:[1,NSNumber(value:length)],dataType:.float32)
    for i in 0..<length {x[i]=NSNumber(value:i<ids.count ? ids[i] : 0);m[i]=NSNumber(value:i<ids.count ? 1.0 : 0.0)}
    return try MLDictionaryFeatureProvider(dictionary:["input_ids":x,"attention_mask":m])
}
func vector(_ output: MLFeatureProvider) -> [Double] {
    let a=output.featureValue(for:"embedding")!.multiArrayValue!
    return (0..<a.count).map{a[$0].doubleValue}
}
func cosine(_ a:[Double],_ b:[Double]) -> Double {
    let dot=zip(a,b).map(*).reduce(0,+)
    return dot/sqrt(a.map{$0*$0}.reduce(0,+)*b.map{$0*$0}.reduce(0,+))
}

@main struct Validate {
    static func main() async throws {
        let args=CommandLine.arguments
        let root=args[1], mode=args[2], multi=args[3]=="multi"
        let lengths=args[4].split(separator:",").map{Int($0)!}
        let fm=FileManager.default
        var result:[String:Any]=["mode":mode,"computeUnits":"cpuAndNeuralEngine","pid":ProcessInfo.processInfo.processIdentifier,"host":ProcessInfo.processInfo.hostName,"os":ProcessInfo.processInfo.operatingSystemVersionString]
        var models:[Int:MLModel]=[:], urls:[Int:URL]=[:], loads:[String:Double]=[:], compiles:[String:Double]=[:], first:[String:Double]=[:]
        try footprint(root,mode+"-start")
        for length in lengths {
            let stem=multi ? "EmbeddingGemma2Owned" : "s\(length)"
            let url=URL(fileURLWithPath:root+"/models/"+stem+".mlmodelc")
            let t=Date()
            if !fm.fileExists(atPath:url.path) {
                let temporary=try await MLModel.compileModel(at:URL(fileURLWithPath:root+"/models/"+stem+".mlpackage"))
                try fm.moveItem(at:temporary,to:url)
            }
            compiles[String(length)]=Date().timeIntervalSince(t)
            let cfg=MLModelConfiguration();cfg.computeUnits = .cpuAndNeuralEngine
            if multi {cfg.functionName="s\(length)"}
            let load=Date();let model=try MLModel(contentsOf:url,configuration:cfg)
            loads[String(length)]=Date().timeIntervalSince(load);models[length]=model;urls[length]=url
            let pred=Date();_ = try await model.prediction(from:provider([2,9259,1],length));first[String(length)]=Date().timeIntervalSince(pred)
            try footprint(root,mode+"-s\(length)")
            print("s\(length) load \(loads[String(length)]!) first \(first[String(length)]!)");fflush(stdout)
        }
        result["compileSeconds"]=compiles;result["loadSeconds"]=loads;result["firstPredictSeconds"]=first
        for length in lengths {_ = try await models[length]!.prediction(from:provider([2,9259,1],length))}
        try footprint(root,mode+"-all-active")
        if mode=="warm" {
            try write(result,root+"/evidence/"+mode+".json")
            return
        }
        let refs=try JSONDecoder().decode([ReferenceRow].self,from:Data(contentsOf:URL(fileURLWithPath:root+"/evidence/hf-reference.json")))
        var parity:[[String:Any]]=[]
        for row in refs where lengths.contains(row.length) {
            let value=vector(try await models[row.length]!.prediction(from:provider(row.tokens,row.length)))
            let finite=value.allSatisfy{$0.isFinite}, norm=sqrt(value.map{$0*$0}.reduce(0,+))
            let c=cosine(value,row.vector)
            let maxAbs=zip(value,row.vector).map{abs($0-$1)}.max()!
            guard value.count==768 && finite && abs(norm-1)<=0.001 && c>0.999 && maxAbs<=0.01 else {throw NSError(domain:"CoreML parity failed",code:1,userInfo:["case":row.case,"cosine":c,"norm":norm,"maxAbs":maxAbs])}
            parity.append(["case":row.case,"length":row.length,"tokens":row.tokens.count,"cosine":c,"norm":norm,"finite":finite,"maxAbs":maxAbs,"vector":value])
        }
        result["hfFP32Parity"]=parity
        print("HF FP32 parity passed for \(parity.count) cases");fflush(stdout)
        if mode=="regression" {
            try write(result,root+"/evidence/"+mode+".json")
            return
        }
        if lengths.count==4 {
            let rows=try JSONDecoder().decode([TokenRow].self,from:Data(contentsOf:URL(fileURLWithPath:root+"/tokens.json")))
            var vectors:[[Double]]=[],indices:[Int]=[],latency:[Double]=[],byBucket:[String:[Double]]=[:]
            var minNorm=Double.infinity,maxNorm=0.0
            for i in 0..<1500 {
                let ids=rows[i*2].tokens
                guard let length=lengths.first(where:{$0>=ids.count}) else {continue}
                let t=Date();let value=vector(try await models[length]!.prediction(from:provider(ids,length)))
                let ms=Date().timeIntervalSince(t)*1000
                let norm=sqrt(value.map{$0*$0}.reduce(0,+));minNorm=min(minNorm,norm);maxNorm=max(maxNorm,norm)
                guard value.count==768 && value.allSatisfy({$0.isFinite}) && abs(norm-1)<=0.001 else {throw NSError(domain:"Invalid FiQA embedding",code:3,userInfo:["document":i,"norm":norm])}
                vectors.append(value);indices.append(i);latency.append(ms);byBucket[String(length),default:[]].append(ms)
            }
            let sorted=latency.sorted()
            result["documents"]=indices.count;result["excludedOver1024"]=1500-indices.count
            result["corpusNormRange"]=[minNorm,maxNorm]
            result["p50Ms"]=sorted[sorted.count/2];result["p95Ms"]=sorted[Int(Double(sorted.count)*0.95)]
            result["docsPerSecond"]=Double(latency.count)*1000/latency.reduce(0,+)
            result["latenciesByBucketMs"]=byBucket
            try write(["indices":indices,"vectors":vectors],root+"/evidence/"+mode+"-vectors.json")
            try footprint(root,mode+"-after-corpus")
        }
        result["phase"]="corpus-complete"
        try write(result,root+"/evidence/"+mode+"-progress.json")
        print("Corpus passed: \(result["documents"] ?? 0) documents");fflush(stdout)
        var plans:[String:Any]=[:]
        for length in lengths {
            let cfg=MLModelConfiguration();cfg.computeUnits = .cpuAndNeuralEngine
            if multi {cfg.functionName="s\(length)"}
            let plan=try await MLComputePlan.load(contentsOf:urls[length]!,configuration:cfg)
            if case let .program(program)=plan.modelStructure {
                var ops:[String:Int]=[:],cost:[String:Double]=[:],cpu:[[String:String]]=[]
                func walk(_ b:MLModelStructure.Program.Block) {
                    for op in b.operations {
                        var device="none"
                        if let usage=plan.deviceUsage(for:op) {switch usage.preferred {case .cpu:device="cpu";case .gpu:device="gpu";case .neuralEngine:device="ane";@unknown default:device="other"}}
                        if device != "none" || op.blocks.isEmpty {ops[device,default:0]+=1;cost[device,default:0]+=plan.estimatedCost(of:op)?.weight ?? 0}
                        if device=="cpu" {cpu.append(["operator":op.operatorName])}
                        for child in op.blocks {walk(child)}
                    }
                }
                for (name,function) in program.functions where !multi || name==cfg.functionName {walk(function.block)}
                guard (ops["ane"] ?? 0)>100 else {throw NSError(domain:"CPU-only graph rejected",code:2)}
                guard !cpu.contains(where: { let name=$0["operator"]!;return name.contains("conv") || name.contains("matmul") || name.contains("linear") || name.contains("softmax") }) else {throw NSError(domain:"CPU attention/FFN core rejected",code:4)}
                plans[String(length)]=["ops":ops,"estimatedCost":cost,"cpuOperators":cpu]
                result["plans"]=plans
                try write(result,root+"/evidence/"+mode+"-progress.json")
                print("Compute plan s\(length): \(ops)");fflush(stdout)
            } else {throw NSError(domain:"Missing program compute plan",code:5)}
        }
        guard plans.count==lengths.count else {throw NSError(domain:"Missing requested function plan",code:6)}
        result["plans"]=plans
        try write(result,root+"/evidence/"+mode+"-progress.json")
        result["phase"]="complete"
        try write(result,root+"/evidence/"+mode+".json")
        print("PASS: stock HF parity and non-CPU-only placement; wrote \(mode).json");fflush(stdout)
        models.removeAll()
    }
}
