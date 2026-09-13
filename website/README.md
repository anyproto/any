# website/ — the any docs site

Markdown in, static HTML and agent-readable Markdown out. `make docs` renders
both formats into `website/dist/` (deploy that folder anywhere static), and
`make docs-serve` previews it. `make docs-check` builds the site and validates
page twins and internal links.

Layout rules (the generator, `cmd/anydocs`, has no config):

- `index.md` at the root is the home page.
- Each `NN-<slug>/` folder is one sidebar section; `NN` orders it, `<slug>`
  becomes the URL path. `index.md` inside is the section's Overview page
  and its `title:` names the section.
- Pages are ordered by `order:` in front-matter, then filename.
- Author front-matter: `title`, `description` (gallery + llms.txt), `order`.
- Links between pages: relative markdown links (`../database/objects.md`
  → written as `../database/objects.html`). Use `.html` in links.
- Raw HTML is allowed (`<div class="cards">…`).
- `_`-prefixed files/folders and `README.md` are skipped.

Every source page produces a human `.html` page and a Markdown twin at the
same path with `.md` as the extension. For example,
`01-understanding/local-first.md` produces both
`/understanding/local-first.html` and `/understanding/local-first.md`. The
generator rewrites internal `.html` destinations to `.md` only in the generated
Markdown; source files and human navigation continue to use `.html`.

Generated Markdown replaces the author front-matter with exactly `title`,
`description`, `canonical_url` (the human HTML page), and
`documentation_index` (`https://docs.any.org/llms.txt`). These generated URL
fields do not belong in source files. `llms.txt` groups every Markdown page by
section and is the global navigation entry point for agents.

Writing style: present tense, describe current behavior only, one idea
per paragraph, a runnable `curl`/CLI example per feature, a "Why this
matters" callout (`> **Why it matters.** …`) where any/anybao differs
from hosted backends.
