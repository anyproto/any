package main

import (
 "encoding/json"
 "fmt"
 "math"
 "os"
 "runtime"
 "time"
 "github.com/hybridgroup/yzma/pkg/llama"
)
type Input struct { ID string `json:"id"`; Text string `json:"text"`; Tokens []int32 `json:"tokens"` }
type Row struct { ID string `json:"id"`; Tokens int `json:"tokens"`; Samples []float64 `json:"samplesMs"`; Vector []float32 `json:"vector"` }
func must(err error) { if err!=nil {panic(err)} }
func main(){
 root:=os.Args[1]
 must(llama.Load(root+"/benchassets/llamacpp-b11600"));must(llama.Init());defer llama.Close()
 if !llama.SupportsGpuOffload(){panic("No GPU offload support")}
 mp:=llama.ModelDefaultParams();mp.NGpuLayers=999
 t:=time.Now();model,err:=llama.ModelLoadFromFile(root+"/benchassets/embeddinggemma-2-Q8_0.gguf",mp);must(err);if model==0 {panic("model nil")};defer llama.ModelFree(model)
 cp:=llama.ContextDefaultParams();cp.NCtx=2048;cp.NBatch=2048;cp.NUbatch=2048;cp.NSeqMax=1;cp.NThreads=7;cp.NThreadsBatch=7;cp.PoolingType=llama.PoolingTypeMean;cp.Embeddings=1;cp.OpOffload=1;cp.Offload_kqv=1
 ctx,err:=llama.InitFromModel(model,cp);must(err);if ctx==0 {panic("context nil")};defer llama.Free(ctx)
 loadSeconds:=time.Since(t).Seconds();initialMemory,err:=llama.GetMemory(ctx);must(err);dim:=llama.ModelNEmbdOut(model);if dim!=768 {panic(fmt.Sprintf("dimension %d",dim))}
 data,err:=os.ReadFile(root+"/evidence/benchmark-inputs.json");must(err);var inputs []Input;must(json.Unmarshal(data,&inputs))
 vocab:=llama.ModelGetVocab(model)
 for _,r:=range inputs {
   // Tokenization is outside timing; stock HF and GGUF must consume identical IDs.
   ids:=llama.Tokenize(vocab,r.Text,true,false)
   if len(ids)==0 || ids[len(ids)-1]!=1 {ids=append(ids,1)}
   if len(ids)!=len(r.Tokens){panic(fmt.Sprintf("token count mismatch %s %d/%d",r.ID,len(ids),len(r.Tokens)))}
   for i,id:=range ids{if int32(id)!=r.Tokens[i]{panic("token mismatch "+r.ID)}}
 }
 infer:=func(ids []int32)([]float32,float64){
   start:=time.Now()
   mem,err:=llama.GetMemory(ctx);must(err);if mem!=0 {must(llama.MemoryClear(mem,false))}
   batch:=llama.BatchInit(int32(len(ids)),0,1)
   for i,id:=range ids {must(batch.Add(llama.Token(id),llama.Pos(i),[]llama.SeqId{0},true))}
   ret,err:=llama.Encode(ctx,batch);must(err);if ret!=0{panic(fmt.Sprintf("encode %d",ret))}
   emb,err:=llama.GetEmbeddingsSeq(ctx,0,dim);must(err);if len(emb)!=768 {panic("empty output")}
   vec:=append([]float32(nil),emb...);var norm float64
   for _,v:=range vec{if math.IsNaN(float64(v))||math.IsInf(float64(v),0){panic("nonfinite")};norm+=float64(v)*float64(v)}
   norm=math.Sqrt(norm);if norm<=0{panic("zero norm")};for i:=range vec{vec[i]/=float32(norm)}
   must(llama.BatchFree(batch))
   ms:=float64(time.Since(start).Nanoseconds())/1e6
   return vec,ms
 }
 rows:=make([]Row,len(inputs));for i,r:=range inputs{rows[i]=Row{ID:r.ID,Tokens:len(r.Tokens)}}
 for round:=0;round<7;round++ {for i,r:=range inputs{v,ms:=infer(r.Tokens);if round>=2{rows[i].Samples=append(rows[i].Samples,ms);rows[i].Vector=v}};fmt.Printf("round %d complete\n",round)}
 result:=map[string]any{"backend":"llama.cpp b11600 Metal Q8_0","api":"llama_encode (encoder-only EG2)","memoryPresent":initialMemory!=0,"memoryClearData":false,"loadSeconds":loadSeconds,"systemInfo":llama.PrintSystemInfo(),"go":runtime.Version(),"contextParams":cp,"modelNGpuLayers":mp.NGpuLayers,"warmupRounds":2,"measuredRounds":5,"rows":rows,"timingBoundary":"pretokenized IDs to copied normalized 768-vector; includes per-call input allocation/fill, state clear, inference synchronization, output copy/L2 and batch free; excludes model load/tokenization"}
 out,err:=json.MarshalIndent(result,"","  ");must(err);must(os.WriteFile(root+"/evidence/metal-warm.json",out,0644))
}
