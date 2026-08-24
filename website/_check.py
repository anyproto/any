import os,re,sys
root=os.path.join(os.path.dirname(os.path.abspath(__file__)),'dist')
bad=0;pages=0
for d,_,fs in os.walk(root):
    for f in fs:
        if not f.endswith('.html'):continue
        p=os.path.join(d,f);pages+=1
        html=open(p,encoding='utf8').read()
        for h in re.findall(r'href="([^"#]+)',html):
            if h.startswith(('http','mailto')):continue
            h=h.split('#')[0]
            t=os.path.normpath(os.path.join(d,h))
            if not os.path.exists(t):
                bad+=1;print('BROKEN',os.path.relpath(p,root),'->',h)
print(f'{pages} pages, {bad} broken links');sys.exit(1 if bad else 0)
