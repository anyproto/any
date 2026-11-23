package coreml

import (
	"errors"
	"strings"
	"testing"
	"time"
)

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

// A plan that comes in time is the caller's, and a handler's error is the
// call's.
func TestAwaitPlan(t *testing.T) {
	done := make(chan planned, 1)
	done <- planned{plan: 7}
	if got, err := awaitPlan(done, "s128", time.Minute, nil); got != 7 || err != nil {
		t.Fatalf("awaitPlan = %d, %v; want the plan", got, err)
	}
	done <- planned{err: "no such function"}
	if _, err := awaitPlan(done, "s128", time.Minute, nil); err == nil || !strings.Contains(err.Error(), "s128: no such function") {
		t.Fatalf("awaitPlan = %v, want the handler's error", err)
	}
}

// A plan the handler has not given in time is a LatePlanError: its Done
// stays open while Core ML is inside the call and closes when the handler
// has run, and the plan that came late is released, not kept.
func TestAwaitPlan_Late(t *testing.T) {
	for _, late := range []planned{{plan: 7}, {err: "failed after all"}} {
		done := make(chan planned, 1)
		released := make(chan uintptr, 1)
		start := time.Now()
		_, err := awaitPlan(done, "s2048", 30*time.Millisecond, func(plan uintptr) { released <- plan })
		var lateErr *LatePlanError
		if !errors.As(err, &lateErr) || lateErr.Function != "s2048" || lateErr.Waited != 30*time.Millisecond {
			t.Fatalf("awaitPlan = %v, want a late plan's error", err)
		}
		if waited := time.Since(start); waited < 30*time.Millisecond {
			t.Fatalf("gave up after %s", waited)
		}
		select {
		case <-lateErr.Done:
			t.Fatal("Done closed with the handler still to run")
		case <-time.After(50 * time.Millisecond):
		}
		done <- late // Core ML calls the handler at last
		select {
		case <-lateErr.Done:
		case <-time.After(10 * time.Second):
			t.Fatal("Done did not close after the handler ran")
		}
		select {
		case plan := <-released:
			if late.plan == 0 || plan != late.plan {
				t.Fatalf("released %d for a late %+v", plan, late)
			}
		default:
			if late.plan != 0 {
				t.Fatal("the plan that came late was kept")
			}
		}
	}
}
