# website/ — the Any docs site

Markdown in, static HTML out. `make docs` renders into `website/dist/`
(deploy that folder anywhere static); `make docs-serve` previews it.

Layout rules (the generator, `cmd/anydocs`, has no config):

- `index.md` at the root is the home page.
- Each `NN-<slug>/` folder is one sidebar section; `NN` orders it, `<slug>`
  becomes the URL path. `index.md` inside is the section's Overview page
  and its `title:` names the section.
- Pages are ordered by `order:` in front-matter, then filename.
- Front-matter: `title`, `description` (gallery + llms.txt), `order`.
- Invalid or unclosed front matter fails the build with the source filename.
- Links between pages: relative markdown links (`../database/objects.md`
  → written as `../database/objects.html`). Use `.html` in links.
- Raw HTML is allowed (`<div class="cards">…`).
- `_`-prefixed files/folders and `README.md` are skipped.

The JavaScript and Python quickstarts link to downloadable clients. The
generator assembles these from the pages' `js` and `python` fenced blocks
into `assets/examples/client.mjs` and `assets/examples/client.py`; keep
those blocks in execution order so the downloads match the guides.
Sources are resolved by their public `/quickstart/javascript.html` and
`/quickstart/python.html` URLs, so changing the numeric section prefix does
not break generation. A linked download without its source page fails the build.

Search indexes every section heading and target, with a shared budget of
4,000 characters of body text per page. Short sections keep their full text;
longer sections share the remaining budget.

Verify the renderer, generated links, assets, fragments, search targets and
downloadable clients from the repository root (requires Go, Node.js and Python 3):

```sh
make docs-check
```

The PR workflow runs this check. Client regression tests use mocked HTTP
responses and streams; they do not start a server or join a sync network.

Writing style: present tense, describe current behavior only, one idea
per paragraph, a runnable `curl`/CLI example per feature, a "Why this
matters" callout (`> **Why it matters.** …`) where any/anybao differs
from hosted backends.

Use **Any** for the product, `any` for the binary and `anyrt` for the
companion runtime. Introduce the local server, database and runtime
separately. Distinguish local embeddings from external model calls, and
file availability from backup durability. Guides identify prerequisites
and explain results; reference pages retain wire formats, limits, errors
and lifecycle behavior.
