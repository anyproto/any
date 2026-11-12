"""Fixed-shape text-only EG2, derived from Transformers v5.19.0 (Apache-2.0).

Uses stock weights without quantization, pruning, or downstream head removal.
Channel-first activations and 1x1 convolutions are export transformations;
pooling before the bias-free projection is algebraically equivalent.
"""
import json
from pathlib import Path

import torch
import torch.nn.functional as F
from safetensors import safe_open


def stock_state(root):
    with safe_open(str(Path(root) / "model.safetensors"), framework="pt") as f:
        return {k.removeprefix("language_model."): f.get_tensor(k).float()
                for k in f.keys() if k.startswith("language_model.")}


class OutputNormalize(torch.nn.Module):
    """Kept in FP32 by the converter: 768 values only, after the FP16 encoder."""
    def forward(self, x):
        x = x / x.abs().amax(dim=-1, keepdim=True).clamp_min(1e-4)
        return x / torch.sqrt((x * x).sum(-1, keepdim=True))


class EG2(torch.nn.Module):
    def __init__(self, root, length):
        super().__init__()
        self.cfg = json.loads((Path(root) / "config.json").read_text())["text_config"]
        self.length = length
        self.eps = self.cfg["rms_norm_eps"]
        self.output_normalize = OutputNormalize()
        self.weights = torch.nn.ParameterDict({k.replace(".", "__"): torch.nn.Parameter(v, requires_grad=False)
                                              for k, v in stock_state(root).items()})
        for kind, dim in [("sliding_attention", 256), ("full_attention", 512)]:
            theta = self.cfg["rope_parameters"][kind]["rope_theta"]
            inv = 1.0 / theta ** (torch.arange(0, dim, 2).float() / dim)
            phase = torch.arange(length).float()[:, None] * inv[None]
            phase = torch.cat([phase, phase], dim=1).T[None, :, None, :]
            self.register_buffer(kind + "_cos", phase.cos())
            self.register_buffer(kind + "_sin", phase.sin())
        r = torch.arange(length)
        self.register_buffer("band", ((r[:, None] - r[None, :]).abs() > self.cfg["sliding_window"]).float()[None, None] * -10000)

    def w(self, key):
        return self.weights[key.replace(".", "__")]

    def linear(self, x, key):
        return F.conv2d(x, self.w(key + ".weight")[:, :, None, None])

    def norm(self, x, key=None):
        # Overflow-safe RMS rewrite adapted from qvac-fabric-llm.cpp
        # export_coreml.py, commit 2ed826df0c0eefc7135e9c3acc45aaa277f9b6ff (MIT).
        scale = x.abs().amax(dim=1, keepdim=True).clamp_min(1.0)
        scaled = x / scale
        y = scaled * torch.rsqrt((scaled * scaled).mean(1, keepdim=True) + self.eps / scale / scale)
        if key:
            y = y * self.w(key + ".weight")[None, :, None, None]
        return y

    @staticmethod
    def rotate(x):
        a, b = x.chunk(2, dim=1)
        return torch.cat([-b, a], dim=1)

    def forward(self, input_ids, attention_mask):
        x = F.embedding(input_ids, self.w("embed_tokens.weight")).transpose(1, 2).unsqueeze(2)
        x = x * self.cfg["hidden_size"] ** 0.5
        embedding = x
        pad = (1.0 - attention_mask[:, None, None, :]) * -10000
        for i, kind in enumerate(self.cfg["layer_types"]):
            base = f"layers.{i}"
            att = base + ".self_attn"
            xn = self.norm(x, base + ".input_layernorm")
            dim = 512 if kind == "full_attention" else 256
            nk = 1 if kind == "full_attention" else 2
            q = self.linear(xn, att + ".q_proj").split(dim, dim=1)
            k = self.linear(xn, att + ".k_proj").split(dim, dim=1)
            v = self.linear(xn, att + ".v_proj").split(dim, dim=1)
            cos, sin = getattr(self, kind + "_cos"), getattr(self, kind + "_sin")
            ks, vs = [], []
            for j in range(nk):
                kj = self.norm(k[j], att + ".k_norm")
                ks.append((kj * cos + self.rotate(kj) * sin).permute(0, 2, 1, 3))
                vs.append(self.norm(v[j]).permute(0, 2, 1, 3))
            mask = pad + self.band if kind == "sliding_attention" else pad
            heads = []
            for j in range(4):
                qj = self.norm(q[j], att + ".q_norm")
                qj = qj * cos + self.rotate(qj) * sin
                scores = torch.matmul(qj.permute(0, 2, 3, 1), ks[j // (4 // nk)])
                prob = torch.softmax(scores + mask, dim=-1)
                h = torch.matmul(vs[j // (4 // nk)], prob.transpose(-1, -2))
                heads.append(h.permute(0, 2, 1, 3))
            a = self.linear(torch.cat(heads, dim=1), att + ".o_proj")
            x = x + self.norm(a, base + ".post_attention_layernorm")
            xn = self.norm(x, base + ".pre_feedforward_layernorm")
            m = F.gelu(self.linear(xn, base + ".mlp.gate_proj"), approximate="tanh") * self.linear(xn, base + ".mlp.up_proj")
            x = x + self.norm(self.linear(m, base + ".mlp.down_proj"), base + ".post_feedforward_layernorm")
            # Split the large PLE projection by layer; preserves stock arithmetic.
            pw = self.w("ple.per_layer_model_projection.weight")[i * 512:(i + 1) * 512]
            ple = F.conv2d(embedding, pw[:, :, None, None]) * self.cfg["hidden_size"] ** -0.5
            ple = self.norm(ple, "ple.per_layer_projection_norm")
            gate = F.gelu(self.linear(x, base + ".ple_block.per_layer_input_gate"), approximate="tanh")
            y = self.linear(gate * ple, base + ".ple_block.per_layer_projection")
            x = (x + self.norm(y, base + ".ple_block.post_per_layer_input_norm")) * self.w(base + ".layer_scalar")
        x = self.norm(x, "norm")
        mask = attention_mask[:, None, None, :]
        x = (x * mask).sum(-1, keepdim=True) / mask.sum(-1, keepdim=True)
        x = self.linear(x, "embedding_projection").flatten(1)
        # Overflow-safe L2 rewrite from the same MIT-licensed exporter.
        return self.output_normalize(x)
