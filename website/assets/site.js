(function(){
  var btn=document.getElementById('mode');if(!btn)return;
  btn.onclick=function(){
    var cur=document.documentElement.dataset.theme;
    if(!cur)cur=matchMedia('(prefers-color-scheme: dark)').matches?'dark':'light';
    var next=cur==='dark'?'light':'dark';
    document.documentElement.dataset.theme=next;
    try{localStorage.setItem('any-docs-theme',next)}catch(e){}
  };
})();
(function(){
  var root=window.__root||'.';
  var q=document.getElementById('q'),res=document.getElementById('results'),idx=null,loading=null,sel=-1;
  var side=document.getElementById('side'),nav=side.querySelector('.side-nav'),menu=document.getElementById('menu');
  // below the breakpoint the sidebar is a drawer; the button, a nav link, a
  // tap on the backdrop and Escape all close it. The root class draws the
  // backdrop (it takes the tap, so nothing underneath is clicked) and locks
  // the page scroll.
  function drawer(open){side.classList.toggle('open',open);document.documentElement.classList.toggle('drawer-open',open);
    menu.setAttribute('aria-expanded',open);if(open)reveal()}
  menu.onclick=function(){drawer(!side.classList.contains('open'))};
  side.addEventListener('click',function(e){if(e.target.closest('a'))drawer(false)});
  document.addEventListener('keydown',function(e){
    if(e.key==='/'&&document.activeElement!==q){e.preventDefault();q.focus();q.select()}
    if(e.key==='Escape'){if(document.activeElement===q)q.value='';res.hidden=true;q.blur();drawer(false)}
  });
  // one fetch shared by every keystroke that arrives before the index does
  function load(){return loading||(loading=fetch(root+'/search.json').then(function(r){return r.json()}).then(function(j){
    j.forEach(function(p){p.t=p.Title.toLowerCase();p.Parts.forEach(function(s){s.h=(s.Heading||'').toLowerCase();s.b=s.Text.toLowerCase()})});
    idx=j},function(e){loading=null;throw e}))}
  function esc(s){return s.replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
  // highlights on the raw text and escapes each piece, so a term never matches
  // inside an entity or an earlier <em>
  function mark(t,terms){var re=new RegExp('('+terms.map(function(w){return w.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')}).join('|')+')','ig');
    return t.split(re).map(function(x,i){return i%2?'<em>'+esc(x)+'</em>':esc(x)}).join('')}
  function snippet(t,terms){var l=t.toLowerCase(),i=-1;for(var k=0;k<terms.length;k++){i=l.indexOf(terms[k]);if(i>=0)break}
    if(i<0)i=0;var s=Math.max(0,i-60);return (s?'…':'')+mark(t.slice(s,s+160),terms)+'…'}
  // occurrences of w in s, stopping at 5 — every score caps a term there
  function count(s,w){var n=0,i=s.indexOf(w);while(i>=0&&n<5){n++;i=s.indexOf(w,i+w.length)}return n}
  // a page ranks on its title and its whole text; the hit links to the
  // section that matches best (the page top when only the title does). One
  // pass per section counts each term once and feeds both scores.
  function search(){var v=q.value.trim().toLowerCase();if(!v){res.hidden=true;return}
    load().then(function(){var terms=v.split(/\s+/);var hits=[];
      idx.forEach(function(p){var score=0,best=p.Parts[0],top=0,sum=terms.map(function(){return 0});
        p.Parts.forEach(function(s){var n=0;terms.forEach(function(w,k){var h=count(s.h,w),b=count(s.b,w);
            sum[k]+=h+b;if(h)n+=3;n+=b});
          if(n>top){top=n;best=s}});
        terms.forEach(function(w,k){if(p.t.indexOf(w)>=0)score+=10;score+=Math.min(sum[k],5)});
        if(score>0)hits.push([score,p,best])});
      hits.sort(function(a,b){return b[0]-a[0]});hits=hits.slice(0,12);sel=-1;
      res.innerHTML=hits.map(function(h){var p=h[1],s=h[2];
        return '<a href="'+root+p.URL+(s.ID?'#'+s.ID:'')+'"><strong>'+esc(p.Title)+'</strong> <small>'+esc(p.Section)+(s.Heading?' › '+esc(s.Heading):'')+'</small><small>'+snippet(s.Text,terms)+'</small></a>'}).join('')||'<a><small>No results</small></a>';
      res.hidden=false})}
  // a picked hit closes the panel and leaves the input, so a same-page
  // anchor jump isn't covered by the results and the bar gets its breadcrumb back
  function done(){res.hidden=true;sel=-1;q.blur();if(res.contains(document.activeElement))document.activeElement.blur()}
  res.addEventListener('click',function(e){if(e.target.closest('a[href]'))done()});
  // the whole pill focuses the input; narrow screens show only its icon
  document.getElementById('search').addEventListener('click',function(e){if(!e.target.closest('.results'))q.focus()});
  q.addEventListener('input',search);q.addEventListener('focus',function(){if(q.value)search()});
  q.addEventListener('keydown',function(e){var as=res.querySelectorAll('a[href]');if(!as.length)return;
    if(e.key==='ArrowDown'){sel=Math.min(sel+1,as.length-1)}else if(e.key==='ArrowUp'){sel=Math.max(sel-1,0)}else if(e.key==='Enter'&&sel>=0){var href=as[sel].href;done();location.href=href;return}else return;
    e.preventDefault();as.forEach(function(a,i){a.classList.toggle('sel',i===sel)})});
  document.addEventListener('click',function(e){if(!e.target.closest('.search'))res.hidden=true;
    if(side.classList.contains('open')&&!e.target.closest('#side,#menu'))drawer(false)});
  // heading anchors
  document.querySelectorAll('.doc h2[id],.doc h3[id]').forEach(function(h){var a=document.createElement('a');a.href='#'+h.id;a.textContent=h.textContent;h.textContent='';h.appendChild(a)});
  // scroll the active sidebar link into the middle of the nav
  function reveal(){var act=nav.querySelector('a.active');if(act)nav.scrollTop=act.offsetTop-nav.offsetTop-nav.clientHeight/2}
  reveal();
})();
(function(){
  var esc=function(s){return s.replace(/&/g,'&amp;').replace(/</g,'&lt;')};
  // Token-first highlighter: protected regions (strings, comments) are matched
  // once and emitted verbatim; keyword/flag rules only ever see the gaps
  // between them, so JSON inside a bash string or a Go struct tag can never
  // pick up keyword colors.
  function hl(t,prot,cls,gap){
    var out='',i=0,m;prot.lastIndex=0;
    while((m=prot.exec(t))){
      out+=gap(esc(t.slice(i,m.index)));
      out+='<span class="'+cls(m[0],prot.lastIndex,t)+'">'+esc(m[0])+'</span>';
      i=prot.lastIndex;
      if(m.index===prot.lastIndex)prot.lastIndex++; // zero-width safety
    }
    return out+gap(esc(t.slice(i)));
  }
  document.querySelectorAll('.doc pre > code').forEach(function(c){
    var m=/language-([\w-]+)/.exec(c.className||'');var lang=m?m[1]:'';c.parentNode.setAttribute('data-lang',lang);
    if(c.parentNode.classList.contains('term'))return;
    if(/^(sh|bash|shell|console|fish|zsh)$/.test(lang)){
      c.innerHTML=c.textContent.split('\n').map(function(l){
        if(/^\s*#/.test(l))return '<span class="c">'+esc(l)+'</span>';
        var o=hl(l,/'[^']*'|"[^"]*"/g,
          function(){return 's'},
          function(g){return g.replace(/(^|\s)(--?[\w][\w-]*)/g,'$1<span class="n">$2</span>')
            .replace(/(\$\{?[A-Za-z_]\w*\}?)/g,'<span class="a">$1</span>')
            .replace(/(\s)(#.*)$/,'$1<span class="c">$2</span>')});
        o=o.replace(/^(\s*)(\$ |&gt; )/,'$1<span class="p">$2</span>');
        o=o.replace(/^(\s*(?:<span class="p">[^<]*<\/span>)?)(any|anyrt|curl|make|go|nix|git|uv|cargo|export|cd|python3|pnpm)\b/,'$1<span class="k">$2</span>');
        return o}).join('\n');
    }else if(/^(json|jsonc|yaml|yml|toml)$/.test(lang)){
      c.innerHTML=hl(c.textContent,/"[^"\n]*"|'[^'\n]*'|#.*$|\/\/.*$/gm,
        function(s,end,t){
          if(s[0]==='#'||s[0]==='/')return 'c';
          return /^\s*:/.test(t.slice(end))?'k':'s'},
        function(g){return g.replace(/\b(true|false|null)\b/g,'<span class="a">$1</span>')
          .replace(/([:\s,\[=])(-?\d+(?:\.\d+)?)(?=[\s,\]}]|$)/g,'$1<span class="a">$2</span>')});
    }else if(/^(py|python)$/.test(lang)){
      c.innerHTML=hl(c.textContent,/"""[\s\S]*?"""|'''[\s\S]*?'''|"[^"\n]*"|'[^'\n]*'|#.*$/gm,
        function(s){return s[0]==='#'?'c':'s'},
        function(g){return g.replace(/\b(def|return|if|elif|else|for|in|while|import|from|as|with|not|and|or|class|try|except|raise|yield|lambda|pass|await|async)\b/g,'<span class="k">$1</span>')
          .replace(/\b(None|True|False)\b/g,'<span class="a">$1</span>')
          .replace(/\b(\d+(?:\.\d+)?)\b/g,'<span class="a">$1</span>')
          .replace(/\b(use|effect|span|print|main)(?=\()/g,'<span class="n">$1</span>')});
    }else if(/^(go|rust|rs|js|javascript|ts|typescript)$/.test(lang)){
      c.innerHTML=hl(c.textContent,/\/\/.*$|`[^`]*`|"[^"\n]*"|'[^'\n]*'/gm,
        function(s){return s[0]==='/'?'c':'s'},
        function(g){return g.replace(/\b(func|let|const|var|return|if|else|for|range|await|async|fn|use|pub|struct|impl|match|import|export|new|type|interface|defer|chan|map|package)\b/g,'<span class="k">$1</span>')
          .replace(/\b(true|false|null|nil|undefined)\b/g,'<span class="a">$1</span>')
          .replace(/\b(\d+(?:\.\d+)?)\b/g,'<span class="a">$1</span>')});
    }
  });
})();
(function(){
  // copy button on every code block. A block written as a terminal session
  // ("$ cmd" lines followed by output) copies only the commands, prompt
  // stripped, with the lines a trailing backslash continues; every other
  // block copies verbatim.
  if(!navigator.clipboard)return;
  document.querySelectorAll('.doc pre > code').forEach(function(c){
    var pre=c.parentNode,b=document.createElement('button');
    b.type='button';b.className='copy';b.textContent='copy';b.setAttribute('aria-label','Copy to clipboard');
    b.onclick=function(){
      var t=c.textContent,cmds=[],cont=false;
      t.split('\n').forEach(function(l){
        if(/^\s*\$ /.test(l))cmds.push(l.replace(/^\s*\$ /,''));else if(!cont)return;else cmds.push(l);
        cont=/\\\s*$/.test(l)});
      if(cmds.length)t=cmds.join('\n');
      navigator.clipboard.writeText(t.replace(/\n$/,'')).then(function(){
        b.textContent='copied';b.classList.add('done');
        setTimeout(function(){b.textContent='copy';b.classList.remove('done')},1500)});
    };
    pre.appendChild(b);
  });
})();
