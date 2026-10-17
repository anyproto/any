package server

import "testing"

func TestValidateLoopback(t *testing.T) {
	cases := []struct {
		addr    string
		wantErr bool
	}{
		{"127.0.0.1:2046", false},
		{"127.0.0.2:2046", false},
		{"[::1]:2046", false},
		{"0.0.0.0:2046", true},
		{"192.168.1.5:2046", true},
		{"localhost:2046", true}, // named hosts rejected
		{"bad", true},            // no port
	}
	for _, tc := range cases {
		err := ValidateLoopback(tc.addr)
		if tc.wantErr != (err != nil) {
			t.Errorf("ValidateLoopback(%q) err=%v wantErr=%v", tc.addr, err, tc.wantErr)
		}
	}
}
