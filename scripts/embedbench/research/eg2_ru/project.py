#!/usr/bin/env python3
"""Apply the frozen exact space edits to a query OR document matrix.

These are diagnostic controls, not recommended new production weights.
Requires stock EG2 vectors; do not apply the head projection twice to an already
quantized patched model when comparing with this exact experiment.
"""
from util import *
import argparse

def main():
 ap=argparse.ArgumentParser(description=__doc__);ap.add_argument('input',type=Path);ap.add_argument('output',type=Path);ap.add_argument('--variant',choices=['head','language','slang','language_random','slang_random'],default='head');a=ap.parse_args()
 assert a.output.resolve().is_relative_to(BASE.resolve()),'Write only under bench/eg2-ru'
 x=np.load(a.input);u=norm(np.load(ROOT/'scripts/embedbench/research/eg2_head_patch/head_mean.npy'));x=x-np.outer(x@u,u)
 if a.variant!='head':
  spec=json.loads((BASE/'FROZEN.json').read_text());d=np.load(BASE/'dev/directions.npz');v=d[a.variant];typ=a.variant.replace('_random','');alpha=spec['projection_doses'][typ];x=x-alpha*np.outer(x@v,v)
 np.save(a.output,norm(x))
if __name__=='__main__':main()
