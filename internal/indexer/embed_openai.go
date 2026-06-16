//go:build vector && !gomobile

package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

const openaiDefaultBaseURL = "https://api.openai.com/v1"

// OpenAI talks to an OpenAI-compatible POST {baseURL}/embeddings API.
// Any gateway implementing that shape works — set BaseUrl accordingly.
type OpenAI struct {
	BaseURL string
	Model   string
	APIKey  string
	HTTP    *http.Client
}

// NewOpenAI returns a client for the given endpoint; model is required
// (the API has no default).
func NewOpenAI(baseURL, model, apiKey string) *OpenAI {
	if baseURL == "" {
		baseURL = openaiDefaultBaseURL
	}
	return &OpenAI{
		BaseURL: baseURL,
		Model:   model,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 2 * time.Minute},
	}
}

type openaiEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openaiEmbedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (c *OpenAI) embed(ctx context.Context, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(openaiEmbedRequest{Model: c.Model, Input: inputs})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return nil, fmt.Errorf("openai embed: status %d: %s", resp.StatusCode, buf.String())
	}
	var out openaiEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(inputs) {
		return nil, fmt.Errorf("openai embed: got %d embeddings for %d inputs", len(out.Data), len(inputs))
	}
	// The API may return entries out of order; index is authoritative.
	sort.Slice(out.Data, func(i, j int) bool { return out.Data[i].Index < out.Data[j].Index })
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

func (c *OpenAI) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	return c.embed(ctx, texts)
}

func (c *OpenAI) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (c *OpenAI) Dim(ctx context.Context) (int, error) {
	v, err := c.EmbedQuery(ctx, "dimension probe")
	if err != nil {
		return 0, err
	}
	return len(v), nil
}
