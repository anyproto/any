package e2e

import (
	"encoding/json"
	"testing"
	"time"
)

// stampMillis reads a derived timestamp off a decoded query row.
//
// Stamps are instants on the wire — `{"$date": "<RFC 3339>"}` — so a
// plain float64 assertion reads as a silent zero. Fails the test when
// the field is absent or not an instant, since every row carries one.
func stampMillis(t *testing.T, row map[string]any, field string) int64 {
	t.Helper()
	obj, ok := row[field].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want an instant object", field, row[field])
	}
	switch v := obj["$date"].(type) {
	case string:
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Fatalf("%s = %q, want RFC 3339: %v", field, v, err)
		}
		return ts.UnixMilli()
	case float64:
		return int64(v)
	}
	t.Fatalf("%s = %#v, want {\"$date\": …}", field, row[field])
	return 0
}

// extDate decodes an extended-JSON instant — `{"$date": "<RFC 3339>"}`,
// or `{"$date": <unix millis>}` for years outside RFC 3339's range.
// Every server-stamped time reads back in this shape.
type extDate struct {
	iso    string
	millis float64
}

func (d *extDate) UnmarshalJSON(raw []byte) error {
	var obj struct {
		Date json.RawMessage `json:"$date"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	if len(obj.Date) == 0 {
		return nil
	}
	if err := json.Unmarshal(obj.Date, &d.iso); err == nil {
		return nil
	}
	return json.Unmarshal(obj.Date, &d.millis)
}

// seconds renders the instant as unix seconds — the resolution the
// stamps come from (a change's timestamp). Zero when absent.
func (d extDate) seconds() int64 {
	if d.iso != "" {
		ts, err := time.Parse(time.RFC3339, d.iso)
		if err != nil {
			return 0
		}
		return ts.Unix()
	}
	return int64(d.millis) / 1000
}
