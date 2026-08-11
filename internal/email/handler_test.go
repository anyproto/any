package email

import (
	"errors"
	"strings"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/handler"
)

const (
	rig      = "rig-account"
	stranger = "someone-else"
)

func makeChange(creator string, ts int64) *handler.Change {
	return &handler.Change{
		SpaceId:   "space",
		ObjectId:  "mailbox",
		Dataset:   Dataset,
		ChangeId:  "ch1",
		VersionId: "v1",
		Creator:   creator,
		Timestamp: ts,
	}
}

// minimalPayload carries the two required fields; tests grow it.
func minimalPayload(a *anyenc.Arena) *anyenc.Value {
	p := a.NewObject()
	p.Set(FieldThreadId, a.NewString("t1"))
	p.Set(FieldInternalDate, a.NewNumberFloat64(1786393890000))
	return p
}

func setRoot(payload *anyenc.Value) *handler.RecordChange {
	return &handler.RecordChange{
		Id:     "19fed5f923c0f962",
		Upsert: true,
		Ops: []handler.Op{{
			Type:    handler.OpSet,
			Path:    nil,
			Payload: payload,
		}},
	}
}

func addr(a *anyenc.Arena, name, address string) *anyenc.Value {
	entry := a.NewObject()
	if name != "" {
		entry.Set(FieldAddrName, a.NewString(name))
	}
	if address != "" {
		entry.Set(FieldAddrAddress, a.NewString(address))
	}
	return entry
}

// existingMessage builds the pre-state anyenc record passed via
// ChangeCtx.Before. Mirrors what BeforeCreate would have stamped.
func existingMessage(a *anyenc.Arena, creator string) *anyenc.Value {
	rec := a.NewObject()
	rec.Set("id", a.NewString("19fed5f923c0f962"))
	rec.Set(FieldCreator, a.NewString(creator))
	rec.Set(FieldThreadId, a.NewString("t1"))
	rec.Set(FieldInternalDate, a.NewNumberFloat64(1786393890000))
	return rec
}

// --- BeforeCreate -----------------------------------------------------------

func TestBeforeCreate_AcceptsFullMessage(t *testing.T) {
	a := &anyenc.Arena{}
	p := minimalPayload(a)
	p.Set(FieldFrom, addr(a, "Sender", "S@Example.COM"))
	to := a.NewArray()
	to.SetArrayItem(0, addr(a, "", "rcpt@example.com"))
	p.Set(FieldTo, to)
	p.Set(FieldSubject, a.NewString("hello"))
	p.Set(FieldDate, a.NewString("Mon, 10 Aug 2026 20:31:30 +0000"))
	p.Set(FieldSnippet, a.NewString("snip"))
	p.Set(FieldBodyText, a.NewString("body"))
	p.Set(FieldBodyTruncated, a.NewFalse())
	labels := a.NewArray()
	labels.SetArrayItem(0, a.NewString("INBOX"))
	p.Set(FieldLabelIds, labels)
	p.Set(FieldHistoryId, a.NewString("8023406"))
	atts := a.NewArray()
	att := a.NewObject()
	att.Set(FieldAttFilename, a.NewString("a.pdf"))
	att.Set(FieldAttMime, a.NewString("application/pdf"))
	att.Set(FieldAttSize, a.NewNumberInt(1024))
	atts.SetArrayItem(0, att)
	p.Set(FieldAttachments, atts)
	p.Set(FieldMessageIdHeader, a.NewString("<m@ex>"))

	ctx := &handler.ChangeCtx{Change: makeChange(rig, 1700000000)}
	if err := (messagesHandler{}).BeforeCreate(ctx, setRoot(p), &handler.Sink{}); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
}

func TestBeforeCreate_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		build  func(a *anyenc.Arena) *anyenc.Value
		wantIn string
	}{
		{
			name: "missing threadId",
			build: func(a *anyenc.Arena) *anyenc.Value {
				p := a.NewObject()
				p.Set(FieldInternalDate, a.NewNumberFloat64(1))
				return p
			},
			wantIn: "threadId required",
		},
		{
			name: "missing internalDate",
			build: func(a *anyenc.Arena) *anyenc.Value {
				p := a.NewObject()
				p.Set(FieldThreadId, a.NewString("t1"))
				return p
			},
			wantIn: "internalDate required",
		},
		{
			name: "internalDate zero",
			build: func(a *anyenc.Arena) *anyenc.Value {
				p := a.NewObject()
				p.Set(FieldThreadId, a.NewString("t1"))
				p.Set(FieldInternalDate, a.NewNumberInt(0))
				return p
			},
			wantIn: "internalDate must be > 0",
		},
		{
			name: "spoofed creator",
			build: func(a *anyenc.Arena) *anyenc.Value {
				p := minimalPayload(a)
				p.Set(FieldCreator, a.NewString("evil"))
				return p
			},
			wantIn: "field_not_allowed: creator",
		},
		{
			name: "forged participants",
			build: func(a *anyenc.Arena) *anyenc.Value {
				p := minimalPayload(a)
				arr := a.NewArray()
				arr.SetArrayItem(0, a.NewString("x@y"))
				p.Set(FieldParticipants, arr)
				return p
			},
			wantIn: "field_not_allowed: participants",
		},
		{
			name: "from missing address",
			build: func(a *anyenc.Arena) *anyenc.Value {
				p := minimalPayload(a)
				p.Set(FieldFrom, addr(a, "Name Only", ""))
				return p
			},
			wantIn: "from.address required",
		},
		{
			name: "to entry unknown sub-field",
			build: func(a *anyenc.Arena) *anyenc.Value {
				p := minimalPayload(a)
				entry := addr(a, "", "a@b")
				entry.Set("ghost", a.NewString("x"))
				arr := a.NewArray()
				arr.SetArrayItem(0, entry)
				p.Set(FieldTo, arr)
				return p
			},
			wantIn: "field_not_allowed: ghost",
		},
		{
			name: "labelIds not strings",
			build: func(a *anyenc.Arena) *anyenc.Value {
				p := minimalPayload(a)
				arr := a.NewArray()
				arr.SetArrayItem(0, a.NewNumberInt(1))
				p.Set(FieldLabelIds, arr)
				return p
			},
			wantIn: "labelIds[0] must be a string",
		},
		{
			name: "attachment missing filename",
			build: func(a *anyenc.Arena) *anyenc.Value {
				p := minimalPayload(a)
				att := a.NewObject()
				att.Set(FieldAttMime, a.NewString("text/plain"))
				arr := a.NewArray()
				arr.SetArrayItem(0, att)
				p.Set(FieldAttachments, arr)
				return p
			},
			wantIn: "filename required",
		},
		{
			name: "unknown top-level field",
			build: func(a *anyenc.Arena) *anyenc.Value {
				p := minimalPayload(a)
				p.Set("rawHtml", a.NewString("<b>no</b>"))
				return p
			},
			wantIn: "field_not_allowed: rawHtml",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &anyenc.Arena{}
			ctx := &handler.ChangeCtx{Change: makeChange(rig, 1700000000)}
			err := (messagesHandler{}).BeforeCreate(ctx, setRoot(tc.build(a)), &handler.Sink{})
			if err == nil {
				t.Fatalf("expected validation error containing %q", tc.wantIn)
			}
			if !errors.Is(err, handler.ErrValidation) {
				t.Errorf("err is not ErrValidation: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("err = %q, want contains %q", err.Error(), tc.wantIn)
			}
		})
	}
}

// --- participants derivation ------------------------------------------------

func TestParticipantsFromPayload(t *testing.T) {
	a := &anyenc.Arena{}
	p := minimalPayload(a)
	p.Set(FieldFrom, addr(a, "S", "Sender@Example.COM"))
	to := a.NewArray()
	to.SetArrayItem(0, addr(a, "", "b@ex.com"))
	to.SetArrayItem(1, addr(a, "", "sender@example.com")) // dup of from, other case
	p.Set(FieldTo, to)
	cc := a.NewArray()
	cc.SetArrayItem(0, addr(a, "", "c@ex.com"))
	p.Set(FieldCc, cc)
	// bcc must NOT contribute.
	bcc := a.NewArray()
	bcc.SetArrayItem(0, addr(a, "", "hidden@ex.com"))
	p.Set(FieldBcc, bcc)

	got := participantsFromPayload(p)
	want := []string{"sender@example.com", "b@ex.com", "c@ex.com"}
	if len(got) != len(want) {
		t.Fatalf("participants = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("participants[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestParticipantsFromPayload_EmptyWhenNoAddresses(t *testing.T) {
	a := &anyenc.Arena{}
	if got := participantsFromPayload(minimalPayload(a)); len(got) != 0 {
		t.Fatalf("participants = %v, want empty", got)
	}
}

// --- BeforeModify -----------------------------------------------------------

func TestBeforeModify_AllowsMutableFields(t *testing.T) {
	a := &anyenc.Arena{}
	before := existingMessage(a, rig)

	labels := a.NewArray()
	labels.SetArrayItem(0, a.NewString("INBOX"))

	cases := []struct {
		name string
		op   handler.Op
	}{
		{"labelIds", handler.Op{Type: handler.OpSet, Path: []string{FieldLabelIds}, Payload: labels}},
		{"historyId", handler.Op{Type: handler.OpSet, Path: []string{FieldHistoryId}, Payload: a.NewString("9000000")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &handler.ChangeCtx{Change: makeChange(rig, 1700000001), Before: before}
			op := tc.op
			if err := (messagesHandler{}).BeforeModify(ctx, nil, &op, &handler.Sink{}); err != nil {
				t.Fatalf("BeforeModify(%s): %v", tc.name, err)
			}
		})
	}
}

func TestBeforeModify_Rejects(t *testing.T) {
	a := &anyenc.Arena{}
	before := existingMessage(a, rig)

	cases := []struct {
		name    string
		creator string
		op      handler.Op
		wantIn  string
	}{
		{
			name:    "immutable field",
			creator: rig,
			op:      handler.Op{Type: handler.OpSet, Path: []string{FieldSubject}, Payload: a.NewString("new")},
			wantIn:  "field_not_modifiable: subject",
		},
		{
			name:    "bodyText immutable",
			creator: rig,
			op:      handler.Op{Type: handler.OpSet, Path: []string{FieldBodyText}, Payload: a.NewString("x")},
			wantIn:  "field_not_modifiable: bodyText",
		},
		{
			name:    "participants immutable",
			creator: rig,
			op:      handler.Op{Type: handler.OpSet, Path: []string{FieldParticipants}, Payload: a.NewArray()},
			wantIn:  "field_not_modifiable: participants",
		},
		{
			name:    "not author",
			creator: stranger,
			op:      handler.Op{Type: handler.OpSet, Path: []string{FieldHistoryId}, Payload: a.NewString("1")},
			wantIn:  "not_author",
		},
		{
			name:    "labelIds wrong type",
			creator: rig,
			op:      handler.Op{Type: handler.OpSet, Path: []string{FieldLabelIds}, Payload: a.NewString("INBOX")},
			wantIn:  "labelIds must be an array",
		},
		{
			name:    "unset not allowed",
			creator: rig,
			op:      handler.Op{Type: handler.OpUnset, Path: []string{FieldLabelIds}},
			wantIn:  "field_not_modifiable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &handler.ChangeCtx{Change: makeChange(tc.creator, 1700000001), Before: before}
			op := tc.op
			err := (messagesHandler{}).BeforeModify(ctx, nil, &op, &handler.Sink{})
			if err == nil {
				t.Fatalf("expected validation error containing %q", tc.wantIn)
			}
			if !errors.Is(err, handler.ErrValidation) {
				t.Errorf("err is not ErrValidation: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("err = %q, want contains %q", err.Error(), tc.wantIn)
			}
		})
	}
}

// --- BeforeDelete -----------------------------------------------------------

func TestBeforeDelete_AuthorOnly(t *testing.T) {
	a := &anyenc.Arena{}
	before := existingMessage(a, rig)

	ctx := &handler.ChangeCtx{Change: makeChange(rig, 1700000002), Before: before}
	if err := (messagesHandler{}).BeforeDelete(ctx, nil, &handler.Sink{}); err != nil {
		t.Fatalf("BeforeDelete by author: %v", err)
	}

	ctx = &handler.ChangeCtx{Change: makeChange(stranger, 1700000002), Before: before}
	if err := (messagesHandler{}).BeforeDelete(ctx, nil, &handler.Sink{}); err == nil {
		t.Fatal("expected not_author rejection")
	}
}

// --- ValidMessageId ---------------------------------------------------------

func TestValidMessageId(t *testing.T) {
	valid := []string{"19fed5f923c0f962", "A-b_9", strings.Repeat("x", MaxMessageIdBytes)}
	for _, id := range valid {
		if !ValidMessageId(id) {
			t.Errorf("ValidMessageId(%q) = false, want true", id)
		}
	}
	invalid := []string{"", "with.dot", "with/slash", "with space", strings.Repeat("x", MaxMessageIdBytes+1)}
	for _, id := range invalid {
		if ValidMessageId(id) {
			t.Errorf("ValidMessageId(%q) = true, want false", id)
		}
	}
}
