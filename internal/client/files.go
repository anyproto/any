package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// Files (files v2). The two byte-carrying calls — FileAttach and
// FileDownload — go through the timeout-less streamHTTP client (a
// multi-GB transfer must not hit Client.http's per-request timeout);
// the caller's ctx is the only deadline. Everything else is the usual
// JSON round-trip via do().

// FileAttachOpts is the client-side metadata of one upload.
type FileAttachOpts struct {
	// Name is the user-facing file name (query param).
	Name string
	// Mime is sent as the request Content-Type; empty sends nothing
	// (the server treats absent/octet-stream as "unset").
	Mime string
	// Variant + VariantOf attach the content as an alternate
	// representation of an existing file on the same object. Both or
	// neither.
	Variant   string
	VariantOf string
}

// FileAttach uploads r as a file bound to objectId — the raw request
// body IS the file, streamed without buffering.
func (c *Client) FileAttach(ctx context.Context, spaceId, objectId string, r io.Reader, opts FileAttachOpts) (*api.FileInfo, error) {
	q := url.Values{}
	if opts.Name != "" {
		q.Set("name", opts.Name)
	}
	if opts.Variant != "" {
		q.Set("variant", opts.Variant)
	}
	if opts.VariantOf != "" {
		q.Set("variantOf", opts.VariantOf)
	}
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/files",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, r)
	if err != nil {
		return nil, err
	}
	if opts.Mime != "" {
		req.Header.Set("Content-Type", opts.Mime)
	}
	resp, err := streamHTTP.Do(req)
	if err != nil {
		return nil, &TransportError{Addr: c.base, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, parseServerError(resp)
	}
	var out api.FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode attach reply: %w", err)
	}
	return &out, nil
}

// FileDownloadResult is an open download: the caller streams Body and
// MUST Close it. Size is -1 when the server didn't send a length.
type FileDownloadResult struct {
	Body io.ReadCloser
	Size int64
	Mime string
	// Name is the filename from Content-Disposition ("" when absent).
	Name string
}

// FileDownload opens GET .../files/:fileId/content — the file's
// plaintext as a raw HTTP body. variant "" downloads the original.
func (c *Client) FileDownload(ctx context.Context, spaceId, fileId, variant string) (*FileDownloadResult, error) {
	path := fmt.Sprintf("/v1/spaces/%s/files/%s/content",
		url.PathEscape(spaceId), url.PathEscape(fileId))
	if variant != "" {
		path += "?variant=" + url.QueryEscape(variant)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := streamHTTP.Do(req)
	if err != nil {
		return nil, &TransportError{Addr: c.base, Err: err}
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, parseServerError(resp)
	}
	res := &FileDownloadResult{
		Body: resp.Body,
		Size: resp.ContentLength,
		Mime: resp.Header.Get("Content-Type"),
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			res.Name = params["filename"]
		}
	}
	return res, nil
}

// FileGet fetches one file's info (the unsealed member view).
func (c *Client) FileGet(ctx context.Context, spaceId, fileId string) (*api.FileInfo, error) {
	var out api.FileInfo
	path := fmt.Sprintf("/v1/spaces/%s/files/%s", url.PathEscape(spaceId), url.PathEscape(fileId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FileList lists the space's files; objectId "" lists everything,
// limit 0 is unlimited.
func (c *Client) FileList(ctx context.Context, spaceId, objectId string, limit int) (*api.FileListResponse, error) {
	q := url.Values{}
	if objectId != "" {
		q.Set("objectId", objectId)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprint(limit))
	}
	path := fmt.Sprintf("/v1/spaces/%s/files", url.PathEscape(spaceId))
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out api.FileListResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FileStatus fetches one file's durability status.
func (c *Client) FileStatus(ctx context.Context, spaceId, fileId string) (*api.FileStatus, error) {
	var out api.FileStatus
	path := fmt.Sprintf("/v1/spaces/%s/files/%s/status", url.PathEscape(spaceId), url.PathEscape(fileId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FileStats fetches the space's aggregate durability counts.
func (c *Client) FileStats(ctx context.Context, spaceId string) (*api.FileStats, error) {
	var out api.FileStats
	path := fmt.Sprintf("/v1/spaces/%s/files/stats", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FilePin schedules a full background fetch of the file's content.
func (c *Client) FilePin(ctx context.Context, spaceId, fileId string) error {
	return c.fileAction(ctx, spaceId, fileId, "pin")
}

// FileRetry makes the file's pending background work due immediately.
func (c *Client) FileRetry(ctx context.Context, spaceId, fileId string) error {
	return c.fileAction(ctx, spaceId, fileId, "retry")
}

// FileOffload drops the file's local bytes (refused with 409
// file.not_durable while they are the only copy).
func (c *Client) FileOffload(ctx context.Context, spaceId, fileId string) error {
	return c.fileAction(ctx, spaceId, fileId, "offload")
}

func (c *Client) fileAction(ctx context.Context, spaceId, fileId, verb string) error {
	path := fmt.Sprintf("/v1/spaces/%s/files/%s/%s",
		url.PathEscape(spaceId), url.PathEscape(fileId), verb)
	return c.do(ctx, http.MethodPost, path, nil, nil)
}

// StreamFileStatusSubscribe opens GET /v1/spaces/:spaceId/files/subscribe
// — SSE of the space's file durability transitions (local ones).
func (c *Client) StreamFileStatusSubscribe(ctx context.Context, spaceId string, fn func(SSEFrame) error) error {
	path := fmt.Sprintf("/v1/spaces/%s/files/subscribe", url.PathEscape(spaceId))
	return c.streamSSE(ctx, http.MethodGet, path, nil, fn)
}

// FilesQuery runs POST .../objects/:objectId/files/query — a snapshot
// over the payload rows of one object's files. body carries the
// standard query fields (filter/sort/limit/offset/includeTotal); nil
// is a plain full snapshot.
func (c *Client) FilesQuery(ctx context.Context, spaceId, objectId string, body []byte) (*api.QueryResponse, error) {
	var out api.QueryResponse
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/files/query",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	// json.RawMessage round-trips through do's json.Marshal unchanged;
	// nil body sends no body at all (a plain full snapshot).
	var reqBody any
	if len(body) > 0 {
		reqBody = json.RawMessage(body)
	}
	if err := c.do(ctx, http.MethodPost, path, reqBody, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamFilesQuerySubscribe opens the live sibling of FilesQuery.
func (c *Client) StreamFilesQuerySubscribe(ctx context.Context, spaceId, objectId string, body []byte, fn func(SSEFrame) error) error {
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/files/query/subscribe",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	return c.streamSSE(ctx, http.MethodPost, path, body, fn)
}

// FileCacheSize fetches the local bytes held by file content across
// all spaces.
func (c *Client) FileCacheSize(ctx context.Context) (*api.FileCacheInfo, error) {
	var out api.FileCacheInfo
	if err := c.do(ctx, http.MethodGet, "/v1/files/cache", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FileCacheFree reclaims at least `bytes` of local file cache (LRU,
// safe-to-drop content only); returns the bytes actually freed.
func (c *Client) FileCacheFree(ctx context.Context, bytes int64) (*api.FileCacheFreeResult, error) {
	var out api.FileCacheFreeResult
	if err := c.do(ctx, http.MethodPost, "/v1/files/cache/free", api.FileCacheFreeRequest{Bytes: bytes}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FileCacheSweep runs one manual file-cache safety pass.
func (c *Client) FileCacheSweep(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/files/cache/sweep", nil, nil)
}
