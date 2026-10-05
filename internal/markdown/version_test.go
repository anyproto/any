package markdown

import "testing"

func TestSavedSeq(t *testing.T) {
	res := SetResult{Inserted: []string{"ins"}, Updated: []string{"upd"}, Deleted: []string{"del"}}
	cases := []struct {
		name   string
		stamps []stamp
		want   uint64
	}{
		{"nothing stamped after the read", nil, 10},
		{"only this save's writes", []stamp{{"upd", 11}, {"ins", 11}, {"del", 12}}, 12},
		{"a foreign write in between", []stamp{{"upd", 11}, {"other", 12}, {"del", 13}}, 10},
	}
	for _, c := range cases {
		if got := savedSeq(10, c.stamps, res); got != c.want {
			t.Errorf("%s: savedSeq = %d, want %d", c.name, got, c.want)
		}
	}
	if got := savedSeq(10, []stamp{{"other", 11}}, SetResult{}); got != 10 {
		t.Errorf("no-op save beside a foreign write: savedSeq = %d, want the read 10", got)
	}
}
