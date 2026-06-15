//go:build vector

package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Ollama defaults — embeddinggemma is a 768-dim local model whose
// retrieval quality depends on the task prefixes below.
const (
	ollamaDefaultURL   = "http://localhost:11434"
	ollamaDefaultModel = "embeddinggemma"
)

// Ollama talks to a local Ollama's /api/embed endpoint (batch input).
type Ollama struct {
	BaseURL string
	Model   string
	HTTP    *http.Client

	// DocPrompt / QueryPrompt are the model's task prefixes; %s is
	// replaced with the content. Empty disables prefixing.
	DocPrompt   string
	QueryPrompt string
}

// NewOllama returns a client with embeddinggemma defaults.
func NewOllama(baseURL, model string) *Ollama {
	if baseURL == "" {
		baseURL = ollamaDefaultURL
	}
	if model == "" {
		model = ollamaDefaultModel
	}
	return &Ollama{
		BaseURL:     baseURL,
		Model:       model,
		HTTP:        &http.Client{Timeout: 5 * time.Minute},
		DocPrompt:   "title: none | text: %s",
		QueryPrompt: "task: search result | query: %s",
	}
}

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (c *Ollama) embed(ctx context.Context, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{Model: c.Model, Input: inputs})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return nil, fmt.Errorf("ollama embed: status %d: %s", resp.StatusCode, buf.String())
	}
	var out ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("ollama embed: got %d embeddings for %d inputs", len(out.Embeddings), len(inputs))
	}
	return out.Embeddings, nil
}

func (c *Ollama) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	return c.embed(ctx, withPrompt(c.DocPrompt, texts))
}

func (c *Ollama) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.embed(ctx, withPrompt(c.QueryPrompt, []string{text}))
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// Dim probes the embedding dimension by embedding a tiny string.
func (c *Ollama) Dim(ctx context.Context) (int, error) {
	v, err := c.EmbedQuery(ctx, "dimension probe")
	if err != nil {
		return 0, err
	}
	return len(v), nil
}

func withPrompt(prompt string, texts []string) []string {
	if prompt == "" {
		return texts
	}
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = fmt.Sprintf(prompt, t)
	}
	return out
}
