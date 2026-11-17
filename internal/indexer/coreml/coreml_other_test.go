//go:build !darwin || !arm64 || ios

package coreml

import (
	"errors"
	"testing"
)

func TestUnsupported(t *testing.T) {
	if _, err := HasNeuralEngine(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("HasNeuralEngine: %v", err)
	}
	if _, err := Compile("m.mlpackage"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Compile: %v", err)
	}
	if _, err := Plan("m.mlmodelc", "s128"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Plan: %v", err)
	}
	if _, err := Open("m.mlmodelc", "s128", 128, 768); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Open: %v", err)
	}
}
