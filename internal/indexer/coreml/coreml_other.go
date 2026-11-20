//go:build !darwin || !arm64 || ios

package coreml

// Function is one loaded function of a compiled model.
type Function struct{}

func HasNeuralEngine() (bool, error)                   { return false, ErrUnsupported }
func Compile(string) (string, error)                   { return "", ErrUnsupported }
func Plan(string, string) (Placement, error)           { return Placement{}, ErrUnsupported }
func PowerState() (Power, error)                       { return Power{}, ErrUnsupported }
func Open(string, string, int, int) (*Function, error) { return nil, ErrUnsupported }
func (*Function) Embed([]int32, []float32) error       { return ErrUnsupported }
func (*Function) Close()                               {}
