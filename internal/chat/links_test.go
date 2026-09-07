package chat

import (
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any/internal/index"
)

func TestMessageLinks(t *testing.T) {
	a := &anyenc.Arena{}
	rec := a.NewObject()
	rec.Set(FieldText, a.NewString("cc [Z](any://m/spc1/ident1), see [spec](any://o/spc1/obj9)"))
	atts := a.NewObject()
	one := a.NewObject()
	one.Set(FieldAttachmentType, a.NewString("link"))
	one.Set(FieldAttachmentLink, a.NewString("any://o/spc1/obj9")) // duplicate of the text link
	atts.Set("a1", one)
	two := a.NewObject()
	two.Set(FieldAttachmentType, a.NewString("image"))
	two.Set(FieldAttachmentLink, a.NewString("any://f/spc1/file3?variant=thumb"))
	atts.Set("a2", two)
	three := a.NewObject()
	three.Set(FieldAttachmentType, a.NewString("bookmark"))
	three.Set(FieldAttachmentLink, a.NewString("https://example.com")) // not ours
	atts.Set("a3", three)
	four := a.NewObject()
	four.Set(FieldAttachmentLink, a.NewString("any://o/obj77?key=abc")) // credential shape: not canonical
	atts.Set("a4", four)
	rec.Set(FieldAttachments, atts)
	agent := a.NewObject()
	agent.Set(FieldAgentDebugLink, a.NewString("any://spacespace1/obj55#turn_3")) // legacy global form, fragment dropped
	rec.Set(FieldAgent, agent)

	got := map[string]string{}
	for _, e := range MessageLinks("spc1", "chat1", "chat_messages", "m1", rec) {
		got[e.Target.String()] = e.Kind
		if e.RecordId != "m1" || e.Dataset != "chat_messages" || e.ObjectId != "chat1" {
			t.Errorf("source place wrong: %+v", e)
		}
	}
	want := map[string]string{
		"any://m/spc1/ident1":       index.LinkKindMention,
		"any://o/spc1/obj9":         index.LinkKindLink,
		"any://f/spc1/file3":        index.LinkKindLink,
		"any://o/spacespace1/obj55": index.LinkKindLink,
	}
	if len(got) != len(want) {
		t.Fatalf("links = %v, want %v", got, want)
	}
	for k, kind := range want {
		if got[k] != kind {
			t.Errorf("%s: kind %q, want %q", k, got[k], kind)
		}
	}
	if MessageLinks("spc1", "chat1", "chat_messages", "m1", nil) != nil {
		t.Error("nil record yields links")
	}
}
