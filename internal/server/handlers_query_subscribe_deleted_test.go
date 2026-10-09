package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// A per-object query/subscribe stream ends with closed{object_deleted}
// once its object is deleted: the SDK closes the subscription with
// ErrObjectDeleted after the removal of the object's rows, and the
// stream maps that reason. Other objects' streams stay open.
func TestServer_QuerySubscribe_ObjectDeletedCloses(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	l := setupSharedLab(t, e)
	l.seed(t, e)
	b, c := l.objs[1], l.objs[2]

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	ownC, ownCDone := openStream(streamCtx, cl.StreamQuerySubscribe, l.spaceId,
		`{"objectId":"`+c+`","dataset":"`+l.samples+`","sort":["score"],"limit":50}`)
	ownB, ownBDone := openStream(streamCtx, cl.StreamQuerySubscribe, l.spaceId,
		`{"objectId":"`+b+`","dataset":"`+l.samples+`","sort":["score"],"limit":50}`)
	if snap := readSnapshot(t, ownC); len(snap.Records) != 1 {
		t.Fatalf("c snapshot = %d records, want 1", len(snap.Records))
	}
	readSnapshot(t, ownB)

	doJSONExpect(t, e, http.MethodDelete, l.base()+"/objects/"+c, http.StatusNoContent)

	// The removal of c's rows may precede the close; the close is the
	// terminal frame either way.
	var closed api.SubscribeClosed
	for {
		f := waitFrame(t, ownC, 10*time.Second)
		if f.Event != "closed" {
			continue
		}
		if err := json.Unmarshal(f.Data, &closed); err != nil {
			t.Fatalf("decode closed: %v (%s)", err, f.Data)
		}
		break
	}
	if closed.Reason != api.SubscribeClosedObjectDeleted {
		t.Fatalf("closed reason = %q, want %q", closed.Reason, api.SubscribeClosedObjectDeleted)
	}
	select {
	case err := <-ownCDone:
		if err != nil {
			t.Fatalf("stream exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not exit after closed")
	}

	// A new stream for the deleted object is refused; the sibling's
	// stream is untouched.
	rec := doJSON(t, e, http.MethodPost, l.base()+"/query/subscribe",
		`{"objectId":"`+c+`","dataset":"`+l.samples+`"}`)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusGone {
		t.Fatalf("subscribe to deleted object: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case f := <-ownB:
		if f.Event == "closed" {
			t.Fatalf("sibling stream closed: %s", f.Data)
		}
	case <-time.After(200 * time.Millisecond):
	}
	streamCancel()
	<-ownBDone
}
