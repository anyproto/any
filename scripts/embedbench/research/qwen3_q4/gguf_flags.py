"""Print the tokenizer and pooling flags of Qwen3-Embedding GGUFs from their headers only.

gguf_flags.py <file.gguf | header bytes> ...

A header is enough: `curl -sL -r 0-16777215 -o x.head <hf resolve url>` fetches the first 16 MB,
which holds the whole metadata block for a 0.6B model. No dependencies.

Qwen3-Embedding pools the last token, which must be EOS 151643, so a usable file needs
`tokenizer.ggml.add_eos_token = true` and `qwen3.pooling_type = 3` (last). The official
Qwen Q8_0 and enacimie's Q4_K_M have both; PeterAM4's Q5_K_M and Q4_K_M-imat ship
add_eos_token = false, which llama.cpp honours: it pools the last content token instead.
"""
import struct
import sys

SCALAR = {0: '<B', 1: '<b', 2: '<H', 3: '<h', 4: '<I', 5: '<i', 6: '<f', 7: '<?', 10: '<Q', 11: '<q', 12: '<d'}
WANT = ['general.name', 'general.file_type', 'tokenizer.ggml.add_eos_token', 'tokenizer.ggml.add_bos_token',
        'tokenizer.ggml.eos_token_id', 'tokenizer.ggml.padding_token_id', 'qwen3.pooling_type']


def metadata(path):
    b = open(path, 'rb').read(64 << 20)
    o = 0

    def take(fmt):
        nonlocal o
        v = struct.unpack_from(fmt, b, o)[0]
        o += struct.calcsize(fmt)
        return v

    def string():
        nonlocal o
        n = take('<Q')
        v = b[o:o + n].decode('utf8', 'replace')
        o += n
        return v

    def value(t):
        nonlocal o
        if t == 8:
            return string()
        if t == 9:
            et, n = take('<I'), take('<Q')
            if et == 8:
                for _ in range(n):
                    string()
            else:
                o += struct.calcsize(SCALAR[et]) * n
            return f'<array {n}>'
        return take(SCALAR[t])

    if b[:4] != b'GGUF':
        raise SystemExit(f'{path}: not a GGUF')
    o = 4
    take('<I'); take('<Q')  # version, tensor count
    out = {}
    for _ in range(take('<Q')):
        k = string()
        out[k] = value(take('<I'))
    return out


if __name__ == '__main__':
    for p in sys.argv[1:]:
        d = metadata(p)
        ok = d.get('tokenizer.ggml.add_eos_token') is True and d.get('qwen3.pooling_type') == 3
        print(('ok  ' if ok else 'BAD ') + p)
        for k in WANT:
            print(f'    {k} = {d.get(k, "-")}')
