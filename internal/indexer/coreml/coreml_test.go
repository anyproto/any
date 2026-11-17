package coreml

import "testing"

func TestPlacement_OnNeuralEngine(t *testing.T) {
	cases := []struct {
		name string
		p    Placement
		want bool
	}{
		{"core on the neural engine, glue on the cpu", Placement{NeuralEngine: 6195, CPU: 14}, true},
		{"cpu only", Placement{CPU: 6209, CPUCore: []string{"ios18.matmul"}}, false},
		{"one core operator on the cpu", Placement{NeuralEngine: 6000, CPU: 15, CPUCore: []string{"ios18.linear"}}, false},
		{"gpu plan", Placement{GPU: 6200, CPU: 9}, false},
		{"empty plan", Placement{}, false},
	}
	for _, c := range cases {
		if got := c.p.OnNeuralEngine(); got != c.want {
			t.Errorf("%s: OnNeuralEngine = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCoreOperator(t *testing.T) {
	for name, want := range map[string]bool{
		"ios18.conv":          true,
		"ios18.matmul":        true,
		"ios18.linear":        true,
		"ios18.softmax":       true,
		"ios18.gather":        false,
		"ios16.reduce_sum":    false,
		"ios18.real_div":      false,
		"ios18.greater_equal": false,
	} {
		if got := coreOperator(name); got != want {
			t.Errorf("coreOperator(%q) = %v, want %v", name, got, want)
		}
	}
}
