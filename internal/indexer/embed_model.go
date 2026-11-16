//go:build llamacpp && !android && !ios

package indexer

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/hybridgroup/yzma/pkg/llama"

	"github.com/anyproto/any/internal/config"
)

// localModel is a pinned model the embedder runs with no further
// config: where its GGUF comes from and how it embeds.
type localModel struct {
	id          string // index.local.model
	file        string
	url         string
	sha256      string
	dim         int    // output width
	pooling     string // poolingType name
	queryPrefix string
	docPrefix   string // may hold titlePlaceholder
	// project is a unit direction removed from every vector
	// (v − u(u·v), renormalized); nil for none.
	project []float32
	// coreml is the model as a Core ML package for the Neural Engine
	// (embed_ane.go); nil for none.
	coreml *coreMLPackage
}

// eg2Head is EmbeddingGemma 2's mean pooled-output direction, 768
// little-endian float32. Removing it from every vector takes out the
// model's short-text bias; it equals patching the model's output head.
//
//go:embed eg2_head.f32
var eg2HeadRaw []byte

var localModels = []localModel{
	{
		id:     config.LocalModelQwen3,
		file:   "Qwen3-Embedding-0.6B-Q8_0.gguf",
		url:    "https://huggingface.co/Qwen/Qwen3-Embedding-0.6B-GGUF/resolve/main/Qwen3-Embedding-0.6B-Q8_0.gguf",
		sha256: "06507c7b42688469c4e7298b0a1e16deff06caf291cf0a5b278c308249c3e439",
		dim:    1024,
		// Qwen3-Embedding pools the trailing EOS. Documents embed bare;
		// queries carry the retrieval instruction (skipping it costs a few
		// points of retrieval quality per the model card).
		pooling:     "last",
		queryPrefix: "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:",
	},
	{
		id:          config.LocalModelEmbeddingGemma2,
		file:        "embeddinggemma-2-Q8_0.gguf",
		url:         "https://huggingface.co/ggml-org/embeddinggemma-2-GGUF/resolve/main/embeddinggemma-2-Q8_0.gguf",
		sha256:      "2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135",
		dim:         768,
		pooling:     "mean",
		queryPrefix: "task: search result | query: ",
		docPrefix:   "title: " + titlePlaceholder + " | text: ",
		project:     decodeFloat32s(eg2HeadRaw),
		coreml: &coreMLPackage{
			name: "embeddinggemma-2",
			url:  "https://huggingface.co/anylab/embeddinggemma-2-coreml/resolve/c27f9012633055fea5bb47fe782ab7d548d77909/EmbeddingGemma2Owned.mlpackage",
			files: []coreMLFile{
				{"Manifest.json", "8f494dc20af594dd81ca1082c50d0db80eae392a84621955de1d1c5c0e99aed2"},
				{"Data/com.apple.CoreML/model.mlmodel", "715c377bda514242cabc6497843ca831d1eec1d84b5e48c6f93a13663e6ce104"},
				{"Data/com.apple.CoreML/weights/weight.bin", "86bf461bce224075ea9ff6fc6a2c2abe50cd701eee58f1d3f5d8e6988d234657"},
			},
			lengths: []int{128, 256, 512, 1024, 2048},
		},
	},
}

// localDefaultModel is the model `index.local.model` unset resolves to.
const localDefaultModel = config.LocalModelQwen3

// lookupLocalModel resolves an index.local.model value; "" is the default.
func lookupLocalModel(id string) (localModel, error) {
	if id == "" {
		id = localDefaultModel
	}
	for _, m := range localModels {
		if m.id == id {
			return m, nil
		}
	}
	return localModel{}, fmt.Errorf("indexer: local embedder: unknown model %q (%s, %s)", id, config.LocalModelQwen3, config.LocalModelEmbeddingGemma2)
}

func decodeFloat32s(raw []byte) []float32 {
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return out
}

// projectOut removes unit direction u from vec in place: v − u(u·v). A
// direction of another width leaves vec alone.
func projectOut(vec, u []float32) {
	if len(u) != len(vec) {
		return
	}
	var dot float64
	for i, x := range vec {
		dot += float64(x) * float64(u[i])
	}
	for i := range vec {
		vec[i] -= float32(dot * float64(u[i]))
	}
}

// poolingType maps a pooling name onto llama.cpp's enum. "" is last-token.
func poolingType(s string) (llama.PoolingType, error) {
	switch s {
	case "", "last":
		return llama.PoolingTypeLast, nil
	case "mean":
		return llama.PoolingTypeMean, nil
	case "cls":
		return llama.PoolingTypeCLS, nil
	}
	return 0, fmt.Errorf("indexer: local embedder: unknown pooling %q (last, mean, cls)", s)
}
