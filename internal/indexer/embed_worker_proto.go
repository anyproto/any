//go:build vector && !gomobile

package indexer

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// Wire format between the server and its `any run embedder` child: a
// magic-prefixed, length-delimited frame carrying a JSON header and an
// optional binary block.
//
//	"ANYE" | uint32 jsonLen | uint32 binLen | json | bin
//
// The magic is what makes a stray write to the child's stdout (a C
// library printing) a clean protocol error the parent recovers from by
// respawning, instead of a mis-decoded vector. Vectors ride the binary
// block as little-endian float32 — a 64x1024 batch is 256 KiB, an order
// of magnitude less than the same numbers as JSON.
const (
	workerFrameMagic  = "ANYE"
	workerFrameHeader = len(workerFrameMagic) + 8
	// Bounds on a declared length, so a desynchronized stream can never
	// make the reader allocate wildly. A doc batch is well under both.
	workerMaxJSON = 64 << 20
	workerMaxBin  = 64 << 20
)

// Frame ops. Requests: embed / dim / close. Responses: ready (the
// handshake, carrying the model dimension) / vectors / dim / error.
const (
	workerOpEmbed   = "embed"
	workerOpDim     = "dim"
	workerOpClose   = "close"
	workerOpReady   = "ready"
	workerOpVectors = "vectors"
	workerOpError   = "error"

	workerRoleDoc   = "doc"
	workerRoleQuery = "query"
)

type workerReq struct {
	Op    string   `json:"op"`
	Role  string   `json:"role,omitempty"`
	Texts []string `json:"texts,omitempty"`
}

type workerResp struct {
	Op    string `json:"op"`
	Dim   int    `json:"dim,omitempty"`
	N     int    `json:"n,omitempty"`
	Error string `json:"error,omitempty"`
}

func writeFrame(w *bufio.Writer, hdr any, bin []byte) error {
	js, err := json.Marshal(hdr)
	if err != nil {
		return fmt.Errorf("indexer: embed worker: encode frame: %w", err)
	}
	var pre [workerFrameHeader]byte
	copy(pre[:], workerFrameMagic)
	binary.LittleEndian.PutUint32(pre[4:], uint32(len(js)))
	binary.LittleEndian.PutUint32(pre[8:], uint32(len(bin)))
	if _, err := w.Write(pre[:]); err != nil {
		return err
	}
	if _, err := w.Write(js); err != nil {
		return err
	}
	if _, err := w.Write(bin); err != nil {
		return err
	}
	return w.Flush()
}

// readFrame decodes the next frame's JSON header into hdr and returns
// its binary block. A bad magic or an out-of-bounds length is a protocol
// error: the stream is unusable and the caller respawns the child.
func readFrame(r *bufio.Reader, hdr any) ([]byte, error) {
	var pre [workerFrameHeader]byte
	if _, err := io.ReadFull(r, pre[:]); err != nil {
		return nil, err
	}
	if string(pre[:len(workerFrameMagic)]) != workerFrameMagic {
		return nil, fmt.Errorf("indexer: embed worker: bad frame magic %q", pre[:len(workerFrameMagic)])
	}
	jsLen := int(binary.LittleEndian.Uint32(pre[4:]))
	binLen := int(binary.LittleEndian.Uint32(pre[8:]))
	if jsLen > workerMaxJSON || binLen > workerMaxBin {
		return nil, fmt.Errorf("indexer: embed worker: frame too large (json %d, bin %d)", jsLen, binLen)
	}
	js := make([]byte, jsLen)
	if _, err := io.ReadFull(r, js); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(js, hdr); err != nil {
		return nil, fmt.Errorf("indexer: embed worker: decode frame: %w", err)
	}
	if binLen == 0 {
		return nil, nil
	}
	bin := make([]byte, binLen)
	if _, err := io.ReadFull(r, bin); err != nil {
		return nil, err
	}
	return bin, nil
}

// encodeVectors flattens equal-length vectors into the binary block and
// reports their dimension (0 for an empty batch).
func encodeVectors(vecs [][]float32) ([]byte, int, error) {
	if len(vecs) == 0 {
		return nil, 0, nil
	}
	dim := len(vecs[0])
	out := make([]byte, 0, len(vecs)*dim*4)
	var buf [4]byte
	for i, v := range vecs {
		if len(v) != dim {
			return nil, 0, fmt.Errorf("indexer: embed worker: vector %d has dim %d, want %d", i, len(v), dim)
		}
		for _, f := range v {
			binary.LittleEndian.PutUint32(buf[:], math.Float32bits(f))
			out = append(out, buf[:]...)
		}
	}
	return out, dim, nil
}

func decodeVectors(bin []byte, n, dim int) ([][]float32, error) {
	if n == 0 {
		return nil, nil
	}
	if n < 0 || dim <= 0 || len(bin) != n*dim*4 {
		return nil, fmt.Errorf("indexer: embed worker: vector block is %d bytes, want %d (n %d, dim %d)", len(bin), n*dim*4, n, dim)
	}
	out := make([][]float32, n)
	for i := range out {
		v := make([]float32, dim)
		for j := range v {
			off := (i*dim + j) * 4
			v[j] = math.Float32frombits(binary.LittleEndian.Uint32(bin[off:]))
		}
		out[i] = v
	}
	return out, nil
}
