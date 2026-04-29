package server

import "testing"

func TestValidateLoopback(t *testing.T) {
	cases := []struct {
		addr    string
		wantErr bool
	}{
		{"127.0.0.1:7001", false},
		{"127.0.0.2:7001", false},
		{"[::1]:7001", false},
		{"0.0.0.0:7001", true},
		{"192.168.1.5:7001", true},
		{"localhost:7001", true}, // named hosts rejected
		{"bad", true},             // no port
	}
	for _, tc := range cases {
		err := ValidateLoopback(tc.addr)
		if tc.wantErr != (err != nil) {
			t.Errorf("ValidateLoopback(%q) err=%v wantErr=%v", tc.addr, err, tc.wantErr)
		}
	}
}
