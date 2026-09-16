package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestPlain(t *testing.T) {
	got := plain(`<p>400 (<code>chat.text_too_long</code>) &amp; <strong>"x"</strong></p><td>a</td><td>b</td>`)
	if want := `400 (chat.text_too_long) & "x" a b`; got != want {
		t.Fatalf("plain = %q, want %q", got, want)
	}
}

func TestSplit(t *testing.T) {
	h := `<h1>Page</h1><p>lead</p>` +
		`<h2 id="one">One <code>x</code></h2><p>first</p>` +
		`<h3 id="two">Two</h3>` +
		`<h2 id="three">Three</h2><p>` + strings.Repeat("long ", 2000) + `tail</p>`
	got := split(h)
	want := []part{
		{Text: "Page lead"},
		{Heading: "One x", ID: "one", Text: "first"},
		{Heading: "Two", ID: "two", Text: ""},
		{Heading: "Three", ID: "three", Text: strings.Repeat("long ", 2000) + "tail"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split = %+v", got)
	}
}
