"""Check a generated docs site's internal links and search targets."""
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urlsplit
import json
import sys

root = Path(sys.argv[1] if len(sys.argv) > 1 else 'website/dist').resolve()

class Page(HTMLParser):
    def __init__(self):
        super().__init__()
        self.ids = set()
        self.links = []
    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if attrs.get('id'):
            self.ids.add(attrs['id'])
        for field in ('href', 'src'):
            if attrs.get(field):
                self.links.append(attrs[field])

pages = {}
for path in root.rglob('*.html'):
    page = Page()
    page.feed(path.read_text())
    pages[path] = page
errors = []
links = 0
for path, page in pages.items():
    for link in page.links:
        url = urlsplit(link)
        if url.scheme or url.netloc:
            continue
        target = (root / unquote(url.path).lstrip('/') if url.path.startswith('/') else path.parent / unquote(url.path)) if url.path else path
        target = target.resolve()
        if target.is_dir():
            target /= 'index.html'
        links += 1
        if not target.exists():
            errors.append(f'{path.relative_to(root)} → {link}: missing file')
        elif url.fragment and target in pages and unquote(url.fragment) not in pages[target].ids:
            errors.append(f'{path.relative_to(root)} → {link}: missing anchor')
index = json.loads((root / 'search.json').read_text())
sections = 0
for page in index:
    target = root / page['URL'].lstrip('/')
    if target not in pages:
        errors.append(f'Search page missing: {page["URL"]}')
        continue
    for part in page['Parts']:
        sections += 1
        if part.get('ID') and part['ID'] not in pages[target].ids:
            errors.append(f'Search anchor missing: {page["URL"]}#{part["ID"]}')
print(json.dumps({'pages': len(pages), 'internal_references': links, 'search_sections': sections, 'errors': errors}, indent=2))
sys.exit(bool(errors))
