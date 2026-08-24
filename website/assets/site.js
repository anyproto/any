(function(){
  var root=window.__root||'.';
  var q=document.getElementById('q'),res=document.getElementById('results'),idx=null,sel=-1;
  document.getElementById('menu').onclick=function(){document.getElementById('side').classList.toggle('open')};
  document.addEventListener('keydown',function(e){
    if(e.key==='/'&&document.activeElement!==q){e.preventDefault();q.focus()}
    if(e.key==='Escape'){res.hidden=true;q.blur()}
  });
  function load(cb){if(idx)return cb();fetch(root+'/search.json').then(function(r){return r.json()}).then(function(j){idx=j;cb()})}
  function esc(s){return s.replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
  function snippet(t,terms){var l=t.toLowerCase(),i=-1;for(var k=0;k<terms.length;k++){i=l.indexOf(terms[k]);if(i>=0)break}
    if(i<0)i=0;var s=Math.max(0,i-60),out=esc(t.slice(s,s+160));
    terms.forEach(function(w){out=out.replace(new RegExp('('+w.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')+')','ig'),'<em>$1</em>')});return (s?'…':'')+out+'…'}
  function search(){var v=q.value.trim().toLowerCase();if(!v){res.hidden=true;return}
    load(function(){var terms=v.split(/\s+/);var hits=[];
      idx.forEach(function(p){var t=p.Title.toLowerCase(),b=p.Text.toLowerCase(),score=0;
        terms.forEach(function(w){if(t.indexOf(w)>=0)score+=10;var c=b.split(w).length-1;score+=Math.min(c,5)});
        if(score>0)hits.push([score,p])});
      hits.sort(function(a,b){return b[0]-a[0]});hits=hits.slice(0,12);sel=-1;
      res.innerHTML=hits.map(function(h){var p=h[1];return '<a href="'+root+p.URL+'"><strong>'+esc(p.Title)+'</strong> <small>'+esc(p.Section)+'</small><small>'+snippet(p.Text,terms)+'</small></a>'}).join('')||'<a><small>No results</small></a>';
      res.hidden=false})}
  q.addEventListener('input',search);q.addEventListener('focus',function(){if(q.value)search()});
  q.addEventListener('keydown',function(e){var as=res.querySelectorAll('a[href]');if(!as.length)return;
    if(e.key==='ArrowDown'){sel=Math.min(sel+1,as.length-1)}else if(e.key==='ArrowUp'){sel=Math.max(sel-1,0)}else if(e.key==='Enter'&&sel>=0){location.href=as[sel].href;return}else return;
    e.preventDefault();as.forEach(function(a,i){a.classList.toggle('sel',i===sel)})});
  document.addEventListener('click',function(e){if(!e.target.closest('.search'))res.hidden=true});
  // heading anchors
  document.querySelectorAll('.doc h2[id],.doc h3[id]').forEach(function(h){var a=document.createElement('a');a.href='#'+h.id;a.textContent=h.textContent;h.textContent='';h.appendChild(a)});
  // scroll active sidebar link into view
  var act=document.querySelector('.side a.active');if(act)act.scrollIntoView({block:'center'});
})();
(function(){
  document.querySelectorAll('.doc pre > code').forEach(function(c){
    var m=/language-([\w-]+)/.exec(c.className||'');var lang=m?m[1]:'';c.parentNode.setAttribute('data-lang',lang);
    if(c.parentNode.classList.contains('term')||!/^(sh|bash|shell|console|fish|zsh)$/.test(lang))return;
    var esc=function(s){return s.replace(/&/g,'&amp;').replace(/</g,'&lt;')};
    c.innerHTML=c.textContent.split('\n').map(function(l){
      if(/^\s*#/.test(l))return '<span class="c">'+esc(l)+'</span>';
      var o=esc(l);
      o=o.replace(/('[^']*'|"[^"]*")/g,'<span class="s">$1</span>');
      o=o.replace(/(\s)(--?[\w-]+)/g,'$1<span class="n">$2</span>');
      o=o.replace(/^(\s*)(\$ |&gt; )/,'$1<span class="p">$2</span>');
      o=o.replace(/^(\s*(?:<span class="p">[^<]*<\/span>)?)(any|anyrt|curl|make|go|nix|git|uv|cargo|export|cd|python3|pnpm)\b/,'$1<span class="k">$2</span>');
      return o}).join('\n');
  });
})();
(function(){
  var esc=function(s){return s.replace(/&/g,'&amp;').replace(/</g,'&lt;')};
  document.querySelectorAll('.doc pre > code').forEach(function(c){
    var lang=c.parentNode.getAttribute('data-lang')||'';
    if(c.parentNode.classList.contains('term'))return;
    if(/^(json|jsonc|yaml|yml|toml)$/.test(lang)){
      c.innerHTML=esc(c.textContent).replace(/(#.*$|\/\/.*$)/gm,'<span class="c">$1</span>')
        .replace(/("[^"\n]*")(\s*:)/g,'<span class="k">$1</span>$2')
        .replace(/(:\s*)("[^"\n]*")/g,'$1<span class="s">$2</span>')
        .replace(/\b(true|false|null)\b/g,'<span class="n">$1</span>')
        .replace(/(:\s*)(-?\d+(?:\.\d+)?)/g,'$1<span class="n">$2</span>');
    }else if(/^(py|python)$/.test(lang)){
      var t=esc(c.textContent),out='',re=/("""[\s\S]*?"""|'''[\s\S]*?'''|"[^"\n]*"|'[^'\n]*'|#.*$)/gm,i=0,m;
      var kw=function(s){return s.replace(/\b(def|return|if|elif|else|for|in|while|import|from|as|with|not|and|or|None|True|False|class|try|except|raise|yield|lambda|pass|await|async)\b/g,'<span class="k">$1</span>').replace(/\b(use|effect|span|print|main)(?=\()/g,'<span class="n">$1</span>')};
      while((m=re.exec(t))){out+=kw(t.slice(i,m.index));out+='<span class="'+(m[0][0]==='#'?'c':'s')+'">'+m[0]+'</span>';i=re.lastIndex}
      c.innerHTML=out+kw(t.slice(i));
    }else if(/^(go|rust|rs|js|javascript|ts|typescript)$/.test(lang)){
      c.innerHTML=esc(c.textContent).replace(/(\/\/.*$)/gm,'<span class="c">$1</span>')
        .replace(/("[^"\n]*"|`[^`]*`|'[^'\n]*')/g,'<span class="s">$1</span>')
        .replace(/\b(func|let|const|var|return|if|else|for|await|async|fn|use|pub|struct|impl|match|import|export|new|type|interface)\b/g,'<span class="k">$1</span>');
    }
  });
})();
