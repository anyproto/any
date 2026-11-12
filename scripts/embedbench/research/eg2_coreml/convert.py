import argparse
import hashlib
import json
import platform
import time
from pathlib import Path

import coremltools as ct
import numpy as np
import torch
from coremltools.converters.mil.mil.scope import ScopeSource

from eg2 import EG2
from fetch_stock import verify
from provenance import fingerprint


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--stock", type=Path, default=Path("stock"))
    p.add_argument("--output", type=Path, default=Path("models"))
    p.add_argument("--lengths", type=int, nargs="+", default=[128, 256, 512, 1024])
    p.add_argument("--merge", action="store_true")
    a = p.parse_args()
    verify(a.stock)
    gate = json.loads(Path("evidence/torch-parity.json").read_text())
    assert gate["passed"], "Stock HF parity must pass before conversion"
    assert gate["provenance"]==fingerprint(), "Stale parity gate: rerun parity.py after source/toolchain changes"
    provenance=fingerprint(conversion=True)
    torch.set_num_threads(4)
    a.output.mkdir(parents=True, exist_ok=True)
    for length in a.lengths:
        path = a.output / f"s{length}.mlpackage"
        metadata=a.output / f"s{length}.provenance.json"
        if path.exists():
            assert metadata.exists() and json.loads(metadata.read_text())==provenance, f"Stale export: use a fresh output directory for {path}"
            print("Existing", path, flush=True)
            continue
        start = time.monotonic()
        model = EG2(a.stock, length).eval()
        ids = torch.zeros((1, length), dtype=torch.int32)
        ids[0, :3] = torch.tensor([2, 9259, 1])
        mask = (ids != 0).float()
        with torch.inference_mode():
            traced = torch.jit.trace(model, (ids, mask), check_trace=False)
        fp32_ops=[]
        def use_fp16(op):
            keep_fp32="output_normalize" in op.scopes.get(ScopeSource.TORCHSCRIPT_MODULE_NAME,[])
            if keep_fp32:fp32_ops.append({"name":op.name,"type":op.op_type})
            return not keep_fp32
        ml = ct.convert(traced, convert_to="mlprogram", minimum_deployment_target=ct.target.macOS15,
                        inputs=[ct.TensorType(name="input_ids", shape=(1, length), dtype=np.int32),
                                ct.TensorType(name="attention_mask", shape=(1, length), dtype=np.float32)],
                        outputs=[ct.TensorType(name="embedding", dtype=np.float32)],
                        compute_precision=ct.transform.FP16ComputePrecision(op_selector=use_fp16),
                        compute_units=ct.ComputeUnit.CPU_AND_NE, skip_model_load=True)
        assert len(fp32_ops)>=3, "FP32 final-normalization scope was lost during conversion"
        ml.author = "Owned conversion from Google stock EG2; see NOTICE"
        ml.license = "Apache-2.0"
        ml.short_description = "Stock EG2 text; FP16 encoder; masked mean including prompt; FP32 final L2"
        ml.user_defined_metadata["stock_revision"] = "914f7f89142e33e77833254d9c9b90c3cef7303b"
        ml.user_defined_metadata["conversion_fingerprint"]=provenance["fingerprint"]
        ml.save(str(path))
        metadata.write_text(json.dumps(provenance,indent=2)+"\n")
        (a.output/f"s{length}.precision.json").write_text(json.dumps({"encoder":"FP16","finalNormalization":"FP32","excludedFromFP16":fp32_ops},indent=2)+"\n")
        print(json.dumps({"length": length, "seconds": time.monotonic() - start, "path": str(path)}), flush=True)
        del model, traced, ml
    if a.merge:
        desc = ct.utils.MultiFunctionDescriptor()
        for length in a.lengths:
            desc.add_function(str(a.output / f"s{length}.mlpackage"), "main", f"s{length}")
        desc.default_function_name = f"s{a.lengths[0]}"
        ct.utils.save_multifunction(desc, str(a.output / "EmbeddingGemma2Owned.mlpackage"))


if __name__ == "__main__":
    main()
