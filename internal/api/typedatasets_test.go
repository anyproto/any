package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSearchTextJSON(t *testing.T) {
	unmarshal := []struct {
		raw     string
		want    SearchText
		wantErr bool
	}{
		{`"body"`, SearchText{"body"}, false},
		{`""`, nil, false}, // empty string = no mapping
		{`["body"]`, SearchText{"body"}, false},
		{`["body","notes"]`, SearchText{"body", "notes"}, false},
		{`[]`, SearchText{}, false}, // non-nil so declaration validation can reject it
		{`42`, nil, true},
		{`["body",42]`, nil, true},
		{`{"k":"v"}`, nil, true},
	}
	for _, tc := range unmarshal {
		var got SearchText
		err := json.Unmarshal([]byte(tc.raw), &got)
		if (err != nil) != tc.wantErr {
			t.Errorf("unmarshal %s: err = %v, wantErr %v", tc.raw, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("unmarshal %s = %#v, want %#v", tc.raw, got, tc.want)
		}
	}

	marshal := []struct {
		in   SearchText
		want string
	}{
		{SearchText{"body"}, `"body"`}, // single key canonicalizes to the bare string
		{SearchText{"body", "notes"}, `["body","notes"]`},
		{nil, `null`}, // struct fields carry omitempty, so this form never rides the wire
	}
	for _, tc := range marshal {
		b, err := json.Marshal(tc.in)
		if err != nil {
			t.Fatalf("marshal %#v: %v", tc.in, err)
		}
		if string(b) != tc.want {
			t.Errorf("marshal %#v = %s, want %s", tc.in, b, tc.want)
		}
	}

	// omitempty drops an absent mapping from the enclosing struct.
	b, err := json.Marshal(DatasetSearchFields{Title: "subject"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"title":"subject"}` {
		t.Errorf("struct marshal = %s, want text omitted", b)
	}
}
