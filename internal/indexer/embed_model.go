//go:build llamacpp && !android && !ios

package indexer

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"hash/fnv"
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
	// rev changes with anything above that moves the model's vectors.
	rev int
	// legacy: indexes written before they recorded their embedder may
	// hold this model's vectors.
	legacy bool
}

// stamp names the vector space a spec embeds into: the pinned model,
// its revision, and the prompts and pooling in effect (config may
// override them). An unnamed GGUF is unknown.
func (s localSpec) stamp() (string, bool) {
	if s.profile == "" {
		return "", true
	}
	m, err := lookupLocalModel(s.profile)
	if err != nil {
		return "", true
	}
	h := fnv.New32a()
	for _, part := range []string{s.pooling, s.queryPrefix, s.docPrefix} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0x1f})
	}
	return fmt.Sprintf("local:%s/%d/%08x", m.id, m.rev, h.Sum32()), m.legacy
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
		rev:         1,
		legacy:      true,
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
		rev:         1,
	},
}

// localDefaultModel is the model `index.local.model` unset resolves to.
const localDefaultModel = config.LocalModelEmbeddingGemma2

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
