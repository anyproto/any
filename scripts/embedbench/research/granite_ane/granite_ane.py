"""Granite Embedding 311M multilingual R2 (ModernBERT) re-implemented for a
fixed-length Core ML / Neural Engine conversion.

Differences from HF's padded eager path, all chosen for fp16 on the ANE:
- additive masks use -1e4 (finite in fp16) instead of finfo.min, so a fully
  masked row (a pad query far from any real token in a local layer) stays
  finite and cannot leak NaN through 0 * NaN in the value matmul;
- LayerNorm eps is configurable (1e-12 underflows to 0 in fp16; pad rows
  whose embedding is all zero would then divide 0 by 0);
- RoPE cos/sin and the sliding-window band are constants for the fixed L;
- the last layer computes only the CLS row (only CLS is pooled).
"""
import math

import torch
import torch.nn.functional as F
from safetensors.torch import load_file

NEG = -1e4


class Granite(torch.nn.Module):
    def __init__(self, hf_dir, L, ln_eps=1e-12, cls_only_last=True, dtype=torch.float32, ln_scale=1.0, gelu='none'):
        super().__init__()
        import json
        cfg = json.load(open(f"{hf_dir}/config.json"))
        self.cfg = cfg
        self.L = L
        self.H = cfg["num_attention_heads"]
        self.C = cfg["hidden_size"]
        self.D = self.C // self.H
        self.nl = cfg["num_hidden_layers"]
        self.every = cfg["global_attn_every_n_layers"]
        self.win = cfg["local_attention"] // 2
        self.eps = ln_eps
        # LN(s*x) == LN(x) while eps is negligible: pre-scaling keeps the
        # squares inside LayerNorm in fp16 range (|hidden| reaches ~7e3).
        self.ln_scale = ln_scale
        self.gelu = gelu  # 'none' = exact erf GELU (the trained one), 'tanh' = approximation
        self.cls_only_last = cls_only_last
        sd = load_file(f"{hf_dir}/model.safetensors")
        sd = {k.removeprefix("model."): v.to(dtype) for k, v in sd.items()}
        self.tok = torch.nn.Parameter(sd["embeddings.tok_embeddings.weight"], requires_grad=False)
        self.emb_norm = torch.nn.Parameter(sd["embeddings.norm.weight"], requires_grad=False)
        self.final_norm = torch.nn.Parameter(sd["final_norm.weight"], requires_grad=False)
        P = torch.nn.ParameterDict()
        for i in range(self.nl):
            for name in ["attn.Wqkv.weight", "attn.Wo.weight", "mlp.Wi.weight", "mlp.Wo.weight", "mlp_norm.weight"] + (["attn_norm.weight"] if i else []):
                P[f"l{i}_" + name.replace(".", "_")] = torch.nn.Parameter(sd[f"layers.{i}.{name}"], requires_grad=False)
        self.P = P
        pos = torch.arange(L, dtype=torch.float64)
        for kind, theta in (("g", cfg["global_rope_theta"]), ("l", cfg["local_rope_theta"])):
            inv = 1.0 / (theta ** (torch.arange(0, self.D, 2, dtype=torch.float64) / self.D))
            fr = torch.outer(pos, inv)
            e = torch.cat([fr, fr], -1)
            self.register_buffer(f"cos_{kind}", e.cos().to(dtype)[None, None], persistent=False)
            self.register_buffer(f"sin_{kind}", e.sin().to(dtype)[None, None], persistent=False)
        r = torch.arange(L)
        band = ((r[None] - r[:, None]).abs() <= self.win)
        self.register_buffer("band", torch.where(band, 0.0, NEG).to(dtype)[None, None], persistent=False)

    def ln(self, x, w):
        if self.ln_scale != 1.0:
            x = x * self.ln_scale
        return F.layer_norm(x, (self.C,), w, None, self.eps)

    @staticmethod
    def rot(x):
        h = x.shape[-1] // 2
        return torch.cat([-x[..., h:], x[..., :h]], -1)

    def attn(self, i, x, kmask, q_rows=None):
        p = self.P
        B, S, _ = x.shape
        qkv = F.linear(x, p[f"l{i}_attn_Wqkv_weight"]).view(B, S, 3, self.H, self.D)
        q, k, v = qkv.permute(2, 0, 3, 1, 4).unbind(0)  # B,H,S,D
        g = i % self.every == 0
        cos, sin = (self.cos_g, self.sin_g) if g else (self.cos_l, self.sin_l)
        if q_rows is not None:
            q = q[:, :, :q_rows]
            qc, qs = cos[:, :, :q_rows], sin[:, :, :q_rows]
        else:
            qc, qs = cos, sin
        q = q * qc + self.rot(q) * qs
        k = k * cos + self.rot(k) * sin
        s = torch.matmul(q, k.transpose(-1, -2)) * (self.D ** -0.5)
        m = kmask if g else kmask + (self.band if q_rows is None else self.band[:, :, :q_rows])
        a = torch.softmax(s + m, -1)
        o = torch.matmul(a, v).transpose(1, 2).reshape(B, -1, self.C)
        return F.linear(o, p[f"l{i}_attn_Wo_weight"])

    def mlp(self, i, x):
        p = self.P
        h = F.linear(x, p[f"l{i}_mlp_Wi_weight"])
        a, g = h.chunk(2, -1)
        return F.linear(F.gelu(a, approximate=self.gelu) * g, p[f"l{i}_mlp_Wo_weight"])

    def forward(self, input_ids, attention_mask, return_hidden=False):
        x = self.ln(F.embedding(input_ids, self.tok), self.emb_norm)
        kmask = ((1.0 - attention_mask.to(x.dtype)) * NEG)[:, None, None, :]
        hs = []
        for i in range(self.nl):
            last = i == self.nl - 1 and self.cls_only_last
            xin = x if i == 0 else self.ln(x, self.P[f"l{i}_attn_norm_weight"])
            if last:
                x = x[:, :1] + self.attn(i, xin, kmask, q_rows=1)
            else:
                x = x + self.attn(i, xin, kmask)
            x = x + self.mlp(i, self.ln(x, self.P[f"l{i}_mlp_norm_weight"]))
            if return_hidden:
                hs.append(x)
        c = self.ln(x[:, 0], self.final_norm)
        if return_hidden:
            return c, hs
        return c


class Pooled(torch.nn.Module):
    """CLS embedding, L2-normalised. `scale` keeps the sum of squares in fp16 range."""

    def __init__(self, g, scale=1.0 / 16):
        super().__init__()
        self.g = g
        self.scale = scale

    def forward(self, input_ids, attention_mask):
        c = self.g(input_ids, attention_mask) * self.scale
        return c / torch.sqrt((c * c).sum(-1, keepdim=True))
