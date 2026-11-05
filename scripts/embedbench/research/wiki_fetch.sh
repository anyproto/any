#!/bin/zsh
# wiki_fetch.sh <out dir>: the 15 Wikipedia articles (EN/DE/RU/JA/ZH, plain-text
# extracts) used as large-block and editor-window stand-ins.
set -eu
mkdir -p $1 && cd $1
fetch() { curl -sf --max-time 30 -A "embedbench-research/0.1" "https://$1.wikipedia.org/w/api.php?action=query&format=json&prop=extracts&explaintext=1&redirects=1&titles=$2" -o "$1-$3.json"; }
fetch en London london; fetch en Photosynthesis photo; fetch en "Python_(programming_language)" python
fetch ru %D0%9C%D0%BE%D1%81%D0%BA%D0%B2%D0%B0 moscow; fetch ru %D0%A4%D0%BE%D1%82%D0%BE%D1%81%D0%B8%D0%BD%D1%82%D0%B5%D0%B7 photo; fetch ru Python python
fetch de Berlin berlin; fetch de Photosynthese photo; fetch de "Python_(Programmiersprache)" python
fetch ja %E6%9D%B1%E4%BA%AC%E9%83%BD tokyo; fetch ja %E5%85%89%E5%90%88%E6%88%90 photo; fetch ja Python python
fetch zh %E5%8C%97%E4%BA%AC%E5%B8%82 beijing; fetch zh %E5%85%89%E5%90%88%E4%BD%9C%E7%94%A8 photo; fetch zh Python python
ls -la
