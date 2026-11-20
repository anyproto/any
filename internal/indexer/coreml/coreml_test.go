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

func TestPlacement_Conclusive(t *testing.T) {
	cases := []struct {
		name string
		p    Placement
		want bool
	}{
		{"every device known", Placement{NeuralEngine: 6195, CPU: 14}, true},
		{"constants without a device", Placement{NeuralEngine: 6195, CPU: 14, Unknown: 900}, true},
		{"a core operator on the cpu, others unknown", Placement{CPU: 1, CPUCore: []string{"ios18.matmul"}, Unknown: 6000, UnknownCore: []string{"ios18.linear"}}, true},
		{"cpu only, a few unknown", Placement{CPU: 6000, Unknown: 50}, true},
		{"a core operator of unknown placement", Placement{NeuralEngine: 6000, Unknown: 1, UnknownCore: []string{"ios18.matmul"}}, false},
		{"no device for any operation", Placement{Unknown: 6209}, false},
		{"too few on the neural engine, enough unknown to change that", Placement{NeuralEngine: 60, CPU: 10, Unknown: 80}, false},
		{"no operation", Placement{}, false},
		{"a core operator unknown, but too few could be on the neural engine", Placement{NeuralEngine: 10, CPU: 6000, Unknown: 1, UnknownCore: []string{"ios18.matmul"}}, true},
		{"unknown ones reach the threshold exactly, which is not past it", Placement{NeuralEngine: 90, CPU: 5000, Unknown: 10, UnknownCore: []string{"ios18.matmul"}}, true},
	}
	for _, c := range cases {
		if got := c.p.Conclusive(); got != c.want {
			t.Errorf("%s: Conclusive = %v, want %v", c.name, got, c.want)
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

func TestPower_Strain(t *testing.T) {
	cases := []struct {
		p    Power
		want string
	}{
		{Power{}, ""},
		{Power{Thermal: ThermalFair}, ""},
		{Power{LowPower: true}, "low power mode"},
		{Power{Thermal: ThermalSerious}, "thermal pressure"},
		{Power{Thermal: ThermalCritical}, "thermal pressure"},
		{Power{LowPower: true, Thermal: ThermalCritical}, "low power mode"},
	}
	for _, c := range cases {
		if got := c.p.Strain(); got != c.want {
			t.Errorf("%+v: Strain = %q, want %q", c.p, got, c.want)
		}
	}
}
