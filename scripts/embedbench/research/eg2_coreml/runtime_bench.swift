import Foundation
import CoreML

struct BenchInput: Decodable {let id:String;let tokens:[Int32]}
func save(_ value:Any,_ path:String)throws{try JSONSerialization.data(withJSONObject:value,options:[.prettyPrinted,.sortedKeys]).write(to:URL(fileURLWithPath:path),options:.atomic)}
func footprint(_ root:String,_ tag:String)throws{
 let p=Process();p.executableURL=URL(fileURLWithPath:"/usr/bin/footprint");p.arguments=["--wired",String(ProcessInfo.processInfo.processIdentifier)]
 let path=root+"/evidence/footprint-"+tag+".txt";FileManager.default.createFile(atPath:path,contents:nil)
 let h=try FileHandle(forWritingTo:URL(fileURLWithPath:path));p.standardOutput=h;p.standardError=h;try p.run();p.waitUntilExit();try h.close()
 guard p.terminationStatus==0 else{throw NSError(domain:"footprint failed",code:Int(p.terminationStatus))}
}
func provider(_ ids:[Int32],_ length:Int)throws->MLFeatureProvider{
 precondition(ids.count<=length)
 let x=try MLMultiArray(shape:[1,NSNumber(value:length)],dataType:.int32),m=try MLMultiArray(shape:[1,NSNumber(value:length)],dataType:.float32)
 for i in 0..<length{x[i]=NSNumber(value:i<ids.count ? ids[i]:0);m[i]=NSNumber(value:i<ids.count ? 1.0:0.0)}
 return try MLDictionaryFeatureProvider(dictionary:["input_ids":x,"attention_mask":m])
}
func infer(_ model:MLModel,_ ids:[Int32],_ length:Int)throws->([Double],Double){
 let start=DispatchTime.now().uptimeNanoseconds
 let vec:[Double]=try autoreleasepool {
  let output=try model.prediction(from:provider(ids,length))
  let a=output.featureValue(for:"embedding")!.multiArrayValue!
  return (0..<a.count).map{a[$0].doubleValue}
 }
 let ms=Double(DispatchTime.now().uptimeNanoseconds-start)/1e6
 let norm=sqrt(vec.map{$0*$0}.reduce(0,+))
 guard vec.count==768 && vec.allSatisfy({$0.isFinite}) && abs(norm-1)<=0.001 else{throw NSError(domain:"Invalid embedding",code:1,userInfo:["norm":norm])}
 return(vec,ms)
}
@main struct RuntimeBench{
 static func main()throws{
  let root=CommandLine.arguments[1],mode=CommandLine.arguments[2]
  let inputs=try JSONDecoder().decode([BenchInput].self,from:Data(contentsOf:URL(fileURLWithPath:root+"/evidence/benchmark-inputs.json")))
  let compiled=URL(fileURLWithPath:root+"/models/EmbeddingGemma2Owned.mlmodelc")
  guard FileManager.default.fileExists(atPath:compiled.path)else{throw NSError(domain:"Compile exact package with validator first",code:2)}
  var result:[String:Any]=["mode":mode,"computeUnits":"cpuAndNeuralEngine","pid":ProcessInfo.processInfo.processIdentifier,"os":ProcessInfo.processInfo.operatingSystemVersionString]
  var models:[Int:MLModel]=[:],loads:[String:Double]=[:]
  let lengths=mode=="memory-pair" ? [128,2048]:[2048]
  try footprint(root,mode+"-baseline")
  for length in lengths{
   let cfg=MLModelConfiguration();cfg.computeUnits = .cpuAndNeuralEngine;cfg.functionName="s\(length)"
   let start=Date()
   models[length]=try autoreleasepool{try MLModel(contentsOf:compiled,configuration:cfg)}
   loads[String(length)]=Date().timeIntervalSince(start)
   _ = try infer(models[length]!,length==128 ? [2,9259,1]:inputs.last!.tokens,length)
  }
  result["loadSeconds"]=loads
  try footprint(root,mode+"-active")
  if mode=="ane-warm"{
   var samples=[[Double]](repeating:[],count:inputs.count),vectors=[[Double]](repeating:[],count:inputs.count)
   for round in 0..<7{
    for (i,row) in inputs.enumerated(){let(v,ms)=try infer(models[2048]!,row.tokens,2048);if round>=2{samples[i].append(ms);vectors[i]=v}}
    print("round \(round) complete");fflush(stdout)
   }
   result["rows"]=inputs.enumerated().map{i,row in ["id":row.id,"tokens":row.tokens.count,"samplesMs":samples[i],"vector":vectors[i]] as [String:Any]}
   result["warmupRounds"]=2;result["measuredRounds"]=5
   result["timingBoundary"]="pretokenized IDs to copied normalized 768-vector; includes per-call MLMultiArray allocation/fill, synchronous prediction, output copy and autorelease; excludes model load/tokenization and validation checks"
  }else{
   guard mode=="memory-alone" || mode=="memory-pair" else{throw NSError(domain:"Unknown mode",code:3)}
   models.removeValue(forKey:2048)
   try footprint(root,mode+"-released2048-immediate")
   Thread.sleep(forTimeInterval:30)
   try footprint(root,mode+"-released2048-30s")
   if let short=models[128]{_ = try infer(short,[2,9259,1],128);try footprint(root,mode+"-short-active-after2048release")}
  }
  models.removeAll()
  try footprint(root,mode+"-released-all-immediate")
  if mode != "ane-warm"{Thread.sleep(forTimeInterval:30);try footprint(root,mode+"-released-all-30s")}
  result["phase"]="complete";result["modelsExplicitlyReleased"]=true
  try save(result,root+"/evidence/"+mode+".json")
 }
}
