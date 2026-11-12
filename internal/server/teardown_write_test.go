package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
)

// A download whose client stopped reading does not hold teardown: the
// gate's write deadline cuts the parked write, the drain completes in
// time and the engine's resources close in-process.
func TestTeardown_StalledDownloadIsCut(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, _, objectId := setupSubscribeFixture(t, e)
	// larger than both loopback socket buffers, so the server's write parks
	big := bytes.Repeat([]byte("0123456789abcdef"), 24<<20/16)
	rec := doRaw(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+objectId+"/files?name=big.bin",
		"application/octet-stream", big)
	if rec.Code != http.StatusCreated {
		t.Fatalf("attach: %d %s", rec.Code, rec.Body.String())
	}
	var info api.FileInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(e)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.(*net.TCPConn).SetReadBuffer(4096)
	fmt.Fprintf(conn, "GET /v1/spaces/%s/files/%s/content HTTP/1.1\r\nHost: test\r\n\r\n", spaceId, info.FileId)
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("download status %q: %v", status, err)
	}
	if _, err := io.ReadFull(br, make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	// stop reading; give the server time to fill the buffers and park
	time.Sleep(500 * time.Millisecond)
	d.gate.mu.Lock()
	parked := d.gate.cur != nil && d.gate.cur.n > 0
	d.gate.mu.Unlock()
	if !parked {
		t.Skip("the socket buffers took the whole download: no write parked")
	}

	start := time.Now()
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedDeauthorized)
	if elapsed := time.Since(start); elapsed > gracefulShutdownDeadline/2 {
		t.Fatalf("teardown took %s behind a stalled download", elapsed)
	}
	if d.sdk != nil {
		t.Fatal("the drain timed out: the engine's resources were left to a straggler")
	}
}
