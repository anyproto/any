package email

import (
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
)

func TestMessageData(t *testing.T) {
	arena := &anyenc.Arena{}

	full := arena.NewObject()
	full.Set("id", arena.NewString("m1"))
	full.Set(FieldSubject, arena.NewString("hello"))
	full.Set(FieldBodyText, arena.NewString("body text"))
	// Addressing present but deliberately excluded from Data.
	full.Set(FieldFrom, arena.NewObject())
	title, data := messageData(full)
	if title != "hello" {
		t.Errorf("title = %q, want %q", title, "hello")
	}
	if data != "hello\nbody text" {
		t.Errorf("data = %q, want %q", data, "hello\nbody text")
	}

	bodyOnly := arena.NewObject()
	bodyOnly.Set(FieldBodyText, arena.NewString("just body"))
	title, data = messageData(bodyOnly)
	if title != "" || data != "just body" {
		t.Errorf("bodyOnly = (%q, %q), want (\"\", \"just body\")", title, data)
	}

	subjectOnly := arena.NewObject()
	subjectOnly.Set(FieldSubject, arena.NewString("subject only"))
	title, data = messageData(subjectOnly)
	if title != "subject only" || data != "subject only" {
		t.Errorf("subjectOnly = (%q, %q)", title, data)
	}

	empty := arena.NewObject()
	if title, data = messageData(empty); title != "" || data != "" {
		t.Errorf("empty = (%q, %q), want empty", title, data)
	}

	if title, data = messageData(nil); title != "" || data != "" {
		t.Errorf("nil = (%q, %q), want empty", title, data)
	}
}
