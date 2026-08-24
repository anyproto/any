# website/ — the any docs site

Markdown in, static HTML out. `make docs` renders into `website/dist/`
(deploy that folder anywhere static); `make docs-serve` previews it.

Layout rules (the generator, `cmd/anydocs`, has no config):

- `index.md` at the root is the home page.
- Each `NN-<slug>/` folder is one sidebar section; `NN` orders it, `<slug>`
  becomes the URL path. `index.md` inside is the section's Overview page
  and its `title:` names the section.
- Pages are ordered by `order:` in front-matter, then filename.
- Front-matter: `title`, `description` (gallery + llms.txt), `order`.
- Links between pages: relative markdown links (`../database/objects.md`
  → written as `../database/objects.html`). Use `.html` in links.
- Raw HTML is allowed (`<div class="cards">…`).
- `_`-prefixed files/folders and `README.md` are skipped.

Writing style: present tense, describe current behavior only, one idea
per paragraph, a runnable `curl`/CLI example per feature, a "Why this
matters" callout (`> **Why it matters.** …`) where any/anybao differs
from hosted backends.
