"""plan_fn.py <multifunction.mlpackage> <fn,fn> : MLComputePlan per function (re-saves with that default)."""
import collections, os, shutil, subprocess, sys, tempfile
import coremltools as ct
from coremltools.models.compute_plan import MLComputePlan
from coremltools.models.compute_device import MLCPUComputeDevice, MLGPUComputeDevice, MLNeuralEngineComputeDevice
pkg, fns = sys.argv[1], sys.argv[2].split(",")
name = lambda d: "ANE" if isinstance(d, MLNeuralEngineComputeDevice) else "GPU" if isinstance(d, MLGPUComputeDevice) else "CPU" if isinstance(d, MLCPUComputeDevice) else "?"
for fn in fns:
    tmp = tempfile.mkdtemp()
    d = ct.utils.MultiFunctionDescriptor(pkg)
    d.default_function_name = fn
    p = os.path.join(tmp, "m.mlpackage"); ct.utils.save_multifunction(d, p)
    subprocess.run(["xcrun", "coremlcompiler", "compile", p, tmp], check=True, capture_output=True)
    plan = MLComputePlan.load_from_path(os.path.join(tmp, "m.mlmodelc"), ct.ComputeUnit.CPU_AND_NE)
    f = plan.model_structure.program.functions[fn]
    cnt, cost, off = collections.Counter(), collections.Counter(), collections.Counter()
    for op in f.block.operations:
        if op.operator_name == "const": continue
        u = plan.get_compute_device_usage_for_mlprogram_operation(op)
        dev = name(u.preferred_compute_device) if u else "none"
        cnt[dev] += 1
        e = plan.get_estimated_cost_for_mlprogram_operation(op); cost[dev] += e.weight if e else 0
        if dev != "ANE": off[op.operator_name] += 1
    tot = sum(cost.values()) or 1
    print(f"{fn}: ops {dict(cnt)} cost " + " ".join(f"{k} {v/tot*100:.1f}%" for k, v in cost.items()) + f" off-ANE {dict(off)}", flush=True)
    shutil.rmtree(tmp)
