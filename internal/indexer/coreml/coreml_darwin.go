//go:build darwin && arm64 && !ios

package coreml

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Core ML enum values: MLComputeUnits and MLMultiArrayDataType.
const (
	computeUnitsCPUAndNeuralEngine = 3
	dataTypeFloat32                = 0x10000 | 32
	dataTypeInt32                  = 0x20000 | 32
)

// planTimeout bounds the wait for a function's compute plan. The first
// plan of a function specializes it: about a minute for the longest one
// on a quiet M2, so the bound leaves room for a slower, busier machine.
const planTimeout = 10 * time.Minute

// Input and output feature names of the embedding functions.
const (
	featureInputIDs  = "input_ids"
	featureMask      = "attention_mask"
	featureEmbedding = "embedding"
)

var (
	loadOnce sync.Once
	loadErr  error

	poolPush          func() uintptr
	poolPop           func(pool uintptr)
	classResponds     func(cls objc.Class, sel objc.SEL) bool
	allComputeDevices func() objc.ID

	clsString, clsURL, clsMutableDictionary, clsMutableArray, clsNumber     objc.Class
	clsModel, clsConfiguration, clsMultiArray, clsProvider, clsFeatureValue objc.Class
	clsPlan, clsNeuralEngine, clsCPU, clsGPU                                objc.Class

	selectors sync.Map // name → objc.SEL
)

func sel(name string) objc.SEL {
	if v, ok := selectors.Load(name); ok {
		return v.(objc.SEL)
	}
	s := objc.RegisterName(name)
	selectors.Store(name, s)
	return s
}

func send(id objc.ID, name string, args ...any) objc.ID {
	return id.Send(sel(name), args...)
}

func classSend(cls objc.Class, name string, args ...any) objc.ID {
	return objc.ID(cls).Send(sel(name), args...)
}

// load binds the frameworks once. Every class and method this package
// calls is checked here, so a Core ML without one of them is a refusal
// and never an unrecognized-selector abort.
func load() error {
	loadOnce.Do(func() { loadErr = loadRuntime() })
	return loadErr
}

func loadRuntime() error {
	// The class checks below run class initializers: one thread, one pool.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const flags = purego.RTLD_LAZY | purego.RTLD_GLOBAL
	libobjc, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", flags)
	if err != nil {
		return fmt.Errorf("%w: load libobjc: %v", ErrUnsupported, err)
	}
	if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", flags); err != nil {
		return fmt.Errorf("%w: load Foundation: %v", ErrUnsupported, err)
	}
	coreML, err := purego.Dlopen("/System/Library/Frameworks/CoreML.framework/CoreML", flags)
	if err != nil {
		return fmt.Errorf("%w: load Core ML: %v", ErrUnsupported, err)
	}
	for _, fn := range []struct {
		dst    any
		handle uintptr
		name   string
	}{
		{&poolPush, libobjc, "objc_autoreleasePoolPush"},
		{&poolPop, libobjc, "objc_autoreleasePoolPop"},
		{&classResponds, libobjc, "class_respondsToSelector"},
		{&allComputeDevices, coreML, "MLAllComputeDevices"},
	} {
		addr, err := purego.Dlsym(fn.handle, fn.name)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrUnsupported, fn.name, err)
		}
		purego.RegisterFunc(fn.dst, addr)
	}
	pool := poolPush()
	defer poolPop(pool)

	for _, c := range []struct {
		dst      *objc.Class
		name     string
		class    []string
		instance []string
	}{
		{&clsString, "NSString", []string{"stringWithUTF8String:"}, []string{"UTF8String"}},
		{&clsURL, "NSURL", []string{"fileURLWithPath:"}, []string{"path"}},
		{&clsMutableDictionary, "NSMutableDictionary", []string{"dictionary"}, []string{"setObject:forKey:", "objectForKey:"}},
		{&clsMutableArray, "NSMutableArray", []string{"array"}, []string{"addObject:", "count", "objectAtIndex:", "lastObject"}},
		{&clsNumber, "NSNumber", []string{"numberWithInteger:"}, []string{"integerValue", "floatValue"}},
		{&clsModel, "MLModel", []string{"modelWithContentsOfURL:configuration:error:"}, []string{"predictionFromFeatures:error:"}},
		{&clsConfiguration, "MLModelConfiguration", nil, []string{"setComputeUnits:", "setFunctionName:"}},
		{&clsMultiArray, "MLMultiArray", nil, []string{"initWithShape:dataType:error:", "dataPointer", "dataType", "count", "shape", "strides", "objectAtIndexedSubscript:"}},
		{&clsProvider, "MLDictionaryFeatureProvider", nil, []string{"initWithDictionary:error:"}},
		{&clsFeatureValue, "MLFeatureValue", []string{"featureValueWithMultiArray:"}, []string{"multiArrayValue"}},
		{&clsPlan, "MLComputePlan", []string{"loadContentsOfURL:configuration:completionHandler:"}, []string{"modelStructure", "computeDeviceUsageForMLProgramOperation:"}},
		{&clsNeuralEngine, "MLNeuralEngineComputeDevice", nil, nil},
		{&clsCPU, "MLCPUComputeDevice", nil, nil},
		{&clsGPU, "MLGPUComputeDevice", nil, nil},
	} {
		cls := objc.GetClass(c.name)
		if cls == 0 {
			return fmt.Errorf("%w: no class %s", ErrUnsupported, c.name)
		}
		for _, name := range c.class {
			if !objc.Send[bool](objc.ID(cls), sel("respondsToSelector:"), sel(name)) {
				return fmt.Errorf("%w: no +[%s %s]", ErrUnsupported, c.name, name)
			}
		}
		for _, name := range c.instance {
			if !classResponds(cls, sel(name)) {
				return fmt.Errorf("%w: no -[%s %s]", ErrUnsupported, c.name, name)
			}
		}
		*c.dst = cls
	}
	return nil
}

// checkString refuses a path or name Foundation cannot hold whole: a nil
// NSString argument raises, an Objective-C exception ends the process,
// and a NUL cuts the C string short.
func checkString(s string) error {
	if s == "" || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return fmt.Errorf("coreml: unusable path or name %q", s)
	}
	return nil
}

// scope runs fn on one OS thread inside an autorelease pool: the pool is
// per thread, and everything fn autoreleases is freed when it returns.
func scope(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := poolPush()
	defer poolPop(pool)
	return fn()
}

func nsString(s string) objc.ID {
	return classSend(clsString, "stringWithUTF8String:", s)
}

func goString(s objc.ID) string {
	if s == 0 {
		return ""
	}
	return objc.Send[string](s, sel("UTF8String"))
}

func fileURL(path string) objc.ID {
	return classSend(clsURL, "fileURLWithPath:", nsString(path))
}

// describe names an NSError by its domain and code; call it while the
// error is alive. Not its description: Core ML puts file URLs in those,
// and what this returns is logged.
func describe(nserr objc.ID) string {
	if nserr == 0 || !responds(nserr, "domain") || !responds(nserr, "code") {
		return "no error reported"
	}
	return fmt.Sprintf("%s error %d", goString(send(nserr, "domain")), objc.Send[int](nserr, sel("code")))
}

func responds(id objc.ID, name string) bool {
	return id != 0 && objc.Send[bool](id, sel("respondsToSelector:"), sel(name))
}

func isKind(id objc.ID, cls objc.Class) bool {
	return id != 0 && objc.Send[bool](id, sel("isKindOfClass:"), cls)
}

// newConfiguration returns an owned configuration for one function on
// the CPU and the Neural Engine.
func newConfiguration(function string) objc.ID {
	cfg := send(classSend(clsConfiguration, "alloc"), "init")
	send(cfg, "setComputeUnits:", computeUnitsCPUAndNeuralEngine)
	send(cfg, "setFunctionName:", nsString(function))
	return cfg
}

// HasNeuralEngine reports whether Core ML lists a Neural Engine among
// this machine's compute devices.
func HasNeuralEngine() (bool, error) {
	if err := load(); err != nil {
		return false, err
	}
	found := false
	err := scope(func() error {
		devices := allComputeDevices()
		for i, n := 0, objc.Send[int](devices, sel("count")); i < n; i++ {
			if isKind(send(devices, "objectAtIndex:", i), clsNeuralEngine) {
				found = true
			}
		}
		return nil
	})
	return found, err
}

// Compile compiles the model package at pkg and returns the directory
// Core ML wrote the compiled model to. The caller moves it into place.
func Compile(pkg string) (string, error) {
	if err := load(); err != nil {
		return "", err
	}
	if err := checkString(pkg); err != nil {
		return "", err
	}
	// Checked here, not at load: a compiled model needs no compile.
	if !objc.Send[bool](objc.ID(clsModel), sel("respondsToSelector:"), sel("compileModelAtURL:error:")) {
		return "", fmt.Errorf("%w: no +[MLModel compileModelAtURL:error:]", ErrUnsupported)
	}
	var out string
	err := scope(func() error {
		var nserr objc.ID
		url := classSend(clsModel, "compileModelAtURL:error:", fileURL(pkg), unsafe.Pointer(&nserr))
		if url == 0 {
			return fmt.Errorf("coreml: compile: %s", describe(nserr))
		}
		out = goString(send(url, "path"))
		return nil
	})
	if err == nil && out == "" {
		err = fmt.Errorf("coreml: compile: no output path")
	}
	return out, err
}

// Function is one loaded function of a compiled model, bound to the CPU
// and the Neural Engine. Its methods are not safe for concurrent use.
type Function struct {
	compiled string
	name     string
	length   int
	dim      int
	model    objc.ID
}

// Open loads function from the compiled model directory. length is the
// function's fixed token count and dim its output width.
func Open(compiled, function string, length, dim int) (*Function, error) {
	if err := load(); err != nil {
		return nil, err
	}
	if err := errors.Join(checkString(compiled), checkString(function)); err != nil {
		return nil, err
	}
	f := &Function{compiled: compiled, name: function, length: length, dim: dim}
	err := scope(func() error {
		cfg := newConfiguration(function)
		defer send(cfg, "release")
		var nserr objc.ID
		model := classSend(clsModel, "modelWithContentsOfURL:configuration:error:", fileURL(compiled), cfg, unsafe.Pointer(&nserr))
		if model == 0 {
			return fmt.Errorf("coreml: load %s: %s", function, describe(nserr))
		}
		f.model = send(model, "retain")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Close releases the model.
func (f *Function) Close() {
	if f == nil || f.model == 0 {
		return
	}
	model := f.model
	f.model = 0
	_ = scope(func() error {
		send(model, "release")
		return nil
	})
}

// newArray returns an owned [1, length] array of dataType with its
// storage, which is contiguous for an array Core ML allocated itself.
func newArray(length, dataType int) (objc.ID, unsafe.Pointer, error) {
	shape := classSend(clsMutableArray, "array")
	send(shape, "addObject:", classSend(clsNumber, "numberWithInteger:", 1))
	send(shape, "addObject:", classSend(clsNumber, "numberWithInteger:", length))
	var nserr objc.ID
	arr := send(classSend(clsMultiArray, "alloc"), "initWithShape:dataType:error:", shape, dataType, unsafe.Pointer(&nserr))
	if arr == 0 {
		return 0, nil, fmt.Errorf("coreml: allocate input: %s", describe(nserr))
	}
	ptr := objc.Send[unsafe.Pointer](arr, sel("dataPointer"))
	if ptr == nil || objc.Send[int](arr, sel("count")) != length {
		send(arr, "release")
		return 0, nil, fmt.Errorf("coreml: allocate input: unexpected storage")
	}
	return arr, ptr, nil
}

// Embed runs the function on tokens, right-padded to its length, and
// writes the model's output vector into out.
func (f *Function) Embed(tokens []int32, out []float32) error {
	if len(tokens) == 0 || len(tokens) > f.length {
		return fmt.Errorf("coreml: %s takes 1..%d tokens, got %d", f.name, f.length, len(tokens))
	}
	if len(out) != f.dim {
		return fmt.Errorf("coreml: %s writes %d values, got room for %d", f.name, f.dim, len(out))
	}
	if f.model == 0 {
		return fmt.Errorf("coreml: %s is closed", f.name)
	}
	return scope(func() error {
		ids, idPtr, err := newArray(f.length, dataTypeInt32)
		if err != nil {
			return err
		}
		defer send(ids, "release")
		mask, maskPtr, err := newArray(f.length, dataTypeFloat32)
		if err != nil {
			return err
		}
		defer send(mask, "release")
		idBuf := unsafe.Slice((*int32)(idPtr), f.length)
		maskBuf := unsafe.Slice((*float32)(maskPtr), f.length)
		clear(idBuf[copy(idBuf, tokens):])
		for i := range maskBuf {
			maskBuf[i] = 0
			if i < len(tokens) {
				maskBuf[i] = 1
			}
		}

		features := classSend(clsMutableDictionary, "dictionary")
		send(features, "setObject:forKey:", classSend(clsFeatureValue, "featureValueWithMultiArray:", ids), nsString(featureInputIDs))
		send(features, "setObject:forKey:", classSend(clsFeatureValue, "featureValueWithMultiArray:", mask), nsString(featureMask))
		var nserr objc.ID
		provider := send(classSend(clsProvider, "alloc"), "initWithDictionary:error:", features, unsafe.Pointer(&nserr))
		if provider == 0 {
			return fmt.Errorf("coreml: %s input: %s", f.name, describe(nserr))
		}
		defer send(provider, "release")

		nserr = 0
		result := send(f.model, "predictionFromFeatures:error:", provider, unsafe.Pointer(&nserr))
		if result == 0 {
			return fmt.Errorf("coreml: %s predict: %s", f.name, describe(nserr))
		}
		if !responds(result, "featureValueForName:") {
			return fmt.Errorf("coreml: %s predict: unreadable output", f.name)
		}
		arr := send(send(result, "featureValueForName:", nsString(featureEmbedding)), "multiArrayValue")
		if arr == 0 {
			return fmt.Errorf("coreml: %s predict: no %q output", f.name, featureEmbedding)
		}
		return readVector(arr, out)
	})
}

// readVector copies a [1, …, n] output array into out. Float32 storage
// is read in place at its stride; any other type reads through
// NSNumber, contiguous storage only.
func readVector(arr objc.ID, out []float32) error {
	if n := objc.Send[int](arr, sel("count")); n != len(out) {
		return fmt.Errorf("coreml: output has %d values, want %d", n, len(out))
	}
	// With the count checked, a last dimension of that size leaves every
	// other dimension at 1.
	if last := objc.Send[int](send(send(arr, "shape"), "lastObject"), sel("integerValue")); last != len(out) {
		return fmt.Errorf("coreml: output is not one row of %d values", len(out))
	}
	stride := objc.Send[int](send(send(arr, "strides"), "lastObject"), sel("integerValue"))
	if stride < 1 {
		return fmt.Errorf("coreml: output has stride %d", stride)
	}
	if objc.Send[int](arr, sel("dataType")) == dataTypeFloat32 {
		if ptr := objc.Send[unsafe.Pointer](arr, sel("dataPointer")); ptr != nil {
			// Strides count elements of the storage.
			for i := range out {
				out[i] = *(*float32)(unsafe.Add(ptr, i*stride*4))
			}
			return nil
		}
	}
	if stride != 1 {
		return fmt.Errorf("coreml: output of type %d with stride %d is not read", objc.Send[int](arr, sel("dataType")), stride)
	}
	for i := range out {
		out[i] = objc.Send[float32](send(arr, "objectAtIndexedSubscript:", i), sel("floatValue"))
	}
	return nil
}

// PowerState reads Low Power Mode and the thermal state from
// NSProcessInfo. A system that answers neither is ErrUnsupported.
func PowerState() (Power, error) {
	var p Power
	if err := load(); err != nil {
		return p, err
	}
	err := scope(func() error {
		cls := objc.GetClass("NSProcessInfo")
		if cls == 0 {
			return fmt.Errorf("%w: no class NSProcessInfo", ErrUnsupported)
		}
		info := classSend(cls, "processInfo")
		if !responds(info, "isLowPowerModeEnabled") || !responds(info, "thermalState") {
			return fmt.Errorf("%w: no power state in NSProcessInfo", ErrUnsupported)
		}
		p.LowPower = objc.Send[bool](info, sel("isLowPowerModeEnabled"))
		p.Thermal = objc.Send[int](info, sel("thermalState"))
		return nil
	})
	return p, err
}

// Plan loads the compute plan of one function of the compiled model and
// counts the preferred device of every operation. It needs no loaded
// function, and costs about what loading one does.
// A plan Core ML does not answer in time is a *LatePlanError.
func Plan(compiled, function string) (Placement, error) {
	var p Placement
	if err := load(); err != nil {
		return p, err
	}
	if err := errors.Join(checkString(compiled), checkString(function)); err != nil {
		return p, err
	}
	err := scope(func() error {
		cfg := newConfiguration(function)
		defer send(cfg, "release")
		type loaded struct {
			plan objc.ID
			err  string
		}
		done := make(chan loaded, 1)
		// Runs on a Core ML queue; the plan is retained for the caller.
		handler := objc.NewBlock(func(_ objc.Block, plan, nserr objc.ID) {
			if plan == 0 {
				done <- loaded{err: describe(nserr)}
				return
			}
			done <- loaded{plan: send(plan, "retain")}
		})
		defer handler.Release()
		classSend(clsPlan, "loadContentsOfURL:configuration:completionHandler:", fileURL(compiled), cfg, handler)
		var plan objc.ID
		select {
		case r := <-done:
			if r.plan == 0 {
				return fmt.Errorf("coreml: compute plan for %s: %s", function, r.err)
			}
			plan = r.plan
		case <-time.After(planTimeout):
			// A plan that still arrives is released, not kept.
			returned := make(chan struct{})
			go func() {
				defer close(returned)
				if r := <-done; r.plan != 0 {
					_ = scope(func() error {
						send(r.plan, "release")
						return nil
					})
				}
			}()
			return &LatePlanError{Function: function, Waited: planTimeout, Done: returned}
		}
		defer send(plan, "release")

		structure := send(plan, "modelStructure")
		if !responds(structure, "program") {
			return fmt.Errorf("coreml: compute plan for %s: unreadable model structure", function)
		}
		program := send(structure, "program")
		if !responds(program, "functions") {
			return fmt.Errorf("coreml: compute plan for %s: not an ML program", function)
		}
		fn := send(send(program, "functions"), "objectForKey:", nsString(function))
		if !responds(fn, "block") {
			return fmt.Errorf("coreml: compute plan for %s: function not in the program", function)
		}
		return walkBlock(plan, send(fn, "block"), &p)
	})
	return p, err
}

// walkBlock counts the operations of one program block and of the blocks
// nested in them. An operation with nested blocks and no device of its
// own is a container and is not counted; one with neither is unknown.
func walkBlock(plan, block objc.ID, p *Placement) error {
	if !responds(block, "operations") {
		return fmt.Errorf("coreml: compute plan: unreadable block")
	}
	ops := send(block, "operations")
	for i, n := 0, objc.Send[int](ops, sel("count")); i < n; i++ {
		op := send(ops, "objectAtIndex:", i)
		if i == 0 && !(responds(op, "operatorName") && responds(op, "blocks")) {
			return fmt.Errorf("coreml: compute plan: unreadable operation")
		}
		var device objc.ID
		if usage := send(plan, "computeDeviceUsageForMLProgramOperation:", op); usage != 0 {
			if !responds(usage, "preferredComputeDevice") {
				return fmt.Errorf("coreml: compute plan: unreadable device usage")
			}
			device = send(usage, "preferredComputeDevice")
		}
		nested := send(op, "blocks")
		blocks := objc.Send[int](nested, sel("count"))
		switch {
		case isKind(device, clsNeuralEngine):
			p.NeuralEngine++
		case isKind(device, clsGPU):
			p.GPU++
		case isKind(device, clsCPU):
			p.CPU++
			if name := goString(send(op, "operatorName")); coreOperator(name) {
				p.CPUCore = append(p.CPUCore, name)
			}
		case blocks == 0:
			p.Unknown++
			if name := goString(send(op, "operatorName")); coreOperator(name) {
				p.UnknownCore = append(p.UnknownCore, name)
			}
		}
		for j, m := 0, blocks; j < m; j++ {
			if err := walkBlock(plan, send(nested, "objectAtIndex:", j), p); err != nil {
				return err
			}
		}
	}
	return nil
}
