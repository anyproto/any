import os
import re
import sys
from urllib.parse import unquote, urlsplit


ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "dist")
DOCS_HOST = "docs.any.org"
HTML_LINK_RE = re.compile(r'href=["\']([^"\']+)')
MARKDOWN_LINK_RE = re.compile(r"\]\(\s*<?([^\s)>]+)>?")
REFERENCE_LINK_RE = re.compile(r"^\s*\[[^]]+\]:\s*<?([^\s>]+)", re.MULTILINE)
FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})")
INLINE_CODE_RE = re.compile(r"(?P<ticks>`+).*?(?P=ticks)")


def generated_files(extension):
    found = set()
    for directory, _, files in os.walk(ROOT):
        for name in files:
            if name.endswith(extension):
                path = os.path.join(directory, name)
                found.add(os.path.relpath(path, ROOT))
    return found


def markdown_without_code(markdown):
    visible = []
    fence = None
    for line in markdown.splitlines():
        match = FENCE_RE.match(line)
        if fence is not None:
            if match and match.group(1)[0] == fence[0] and len(match.group(1)) >= len(fence):
                fence = None
            continue
        if match:
            fence = match.group(1)
            continue
        visible.append(INLINE_CODE_RE.sub("", line))
    return "\n".join(visible)


def links_in(path):
    with open(path, encoding="utf8") as source:
        content = source.read()
    if path.endswith(".html"):
        return HTML_LINK_RE.findall(content)
    markdown = markdown_without_code(content)
    return (
        MARKDOWN_LINK_RE.findall(markdown)
        + REFERENCE_LINK_RE.findall(markdown)
        + HTML_LINK_RE.findall(markdown)
    )


def local_target(source, target):
    parsed = urlsplit(target)
    if parsed.netloc and parsed.hostname != DOCS_HOST:
        return None
    if parsed.scheme and parsed.scheme not in ("http", "https"):
        return None
    path = unquote(parsed.path)
    if not path:
        return None
    if path.startswith("/") or parsed.hostname == DOCS_HOST:
        return os.path.normpath(os.path.join(ROOT, path.lstrip("/")))
    return os.path.normpath(os.path.join(os.path.dirname(source), path))


def check_link(source, target):
    resolved = local_target(source, target)
    if (
        resolved is not None
        and (source.endswith(".md") or source.endswith("llms.txt"))
        and urlsplit(target).path.endswith(".html")
    ):
        print("HTML LINK IN MARKDOWN", os.path.relpath(source, ROOT), "->", target)
        return False
    if resolved is None or os.path.exists(resolved):
        return True
    print("BROKEN", os.path.relpath(source, ROOT), "->", target)
    return False


def main():
    html_files = generated_files(".html")
    markdown_files = generated_files(".md")
    bad = 0

    html_stems = {os.path.splitext(path)[0] for path in html_files}
    markdown_stems = {os.path.splitext(path)[0] for path in markdown_files}
    for stem in sorted(html_stems - markdown_stems):
        bad += 1
        print("MISSING MARKDOWN TWIN", stem + ".html", "->", stem + ".md")
    for stem in sorted(markdown_stems - html_stems):
        bad += 1
        print("MISSING HTML TWIN", stem + ".md", "->", stem + ".html")

    checked = [os.path.join(ROOT, path) for path in sorted(html_files | markdown_files)]
    llms = os.path.join(ROOT, "llms.txt")
    if os.path.exists(llms):
        checked.append(llms)
    else:
        bad += 1
        print("MISSING llms.txt")

    for source in checked:
        for target in links_in(source):
            if not check_link(source, target):
                bad += 1

    print(
        f"{len(html_files)} HTML pages, {len(markdown_files)} Markdown pages, "
        f"{bad} problems"
    )
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
