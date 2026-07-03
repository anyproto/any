#!/usr/bin/env node
// import-obsidian.mjs — import an Obsidian vault into an `any` space as a
// typed knowledge base, using only the HTTP API (never the SDK directly).
//
// Usage:
//   node scripts/import-obsidian.mjs --vault ../my-obsidian-vault/myzettel --space "bobrik"
//   node scripts/import-obsidian.mjs --vault ../my-obsidian-vault/myzettel --create-space "Zettelkasten"
//   [--base http://127.0.0.1:7001] [--dry-run]
//
// --space takes a space NAME (not id) and resolves to the single active,
// non-deleted space with that name. Ambiguous (multiple active matches)
// or no match is a hard error listing the candidates.
//
// What it does:
//   1. Scans the vault (skips .obsidian/.trash/.git/Assets, README.md, and
//      dashboard Home.md files), parsing frontmatter, inline dataview fields
//      ((date:: ...)), and [[wikilinks]].
//   2. Creates a small type system mapped from the vault's `template:` keys.
//      Every imported object carries the shared base type `zettel`
//      (source / created / tags / links / topics / aliases) plus zero or
//      more specific types (topic, course, book, article, person, problem,
//      daily, weekly, monthly, trip, ticket, event) — multityping in action.
//   3. Mirrors the vault folder structure as nav folders.
//   4. Creates one object per note: typed properties from frontmatter,
//      `any.name` from the filename, cleaned markdown body via
//      PUT /editor/markdown (dataview blocks, banner images, and the
//      "Internal Links" footer are stripped — links live in properties).
//
// Wikilinks are resolved against the vault's own file titles + aliases and
// stored on `zettel.links` (all resolved targets) and `zettel.topics`
// (targets that are MOC pages, plus the `parent:` frontmatter), as object
// names — query them with array filters, see docs/09-query.md.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import process from "node:process";

// ---------------------------------------------------------------- args

const args = process.argv.slice(2);
function flag(name) {
  const i = args.indexOf(name);
  if (i === -1) return undefined;
  return args[i + 1];
}
const VAULT = flag("--vault");
const BASE = flag("--base") ?? "http://127.0.0.1:7001";
const SPACE_NAME = flag("--space");
let SPACE; // resolved id, filled in main()
const CREATE_SPACE = flag("--create-space");
const DRY = args.includes("--dry-run");

if (!VAULT || (!SPACE_NAME && !CREATE_SPACE && !DRY)) {
  console.error(
    "usage: import-obsidian.mjs --vault <dir> (--space <name> | --create-space <name>) [--base <url>] [--dry-run]",
  );
  process.exit(1);
}

// ---------------------------------------------------------------- http

async function api(method, path, body) {
  const res = await fetch(BASE + path, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  if (!res.ok) {
    throw new Error(`${method} ${path} → ${res.status}: ${text.slice(0, 400)}`);
  }
  return text ? JSON.parse(text) : null;
}

// Resolve a space NAME to the single active (non-deleted) space id with
// that name. Errors — with the candidate list — on zero or multiple
// matches, so the caller never imports into an ambiguous target.
async function resolveSpaceByName(name) {
  const { spaces } = await api("GET", "/v1/spaces");
  const matches = spaces.filter(
    (s) => s.name === name && s.status !== "deleted",
  );
  if (matches.length === 1) return matches[0].id;
  if (matches.length === 0) {
    const names = [
      ...new Set(
        spaces.filter((s) => s.status !== "deleted").map((s) => s.name),
      ),
    ].sort();
    throw new Error(
      `no active space named "${name}". active space names: ${names.join(", ")}`,
    );
  }
  throw new Error(
    `ambiguous: ${matches.length} active spaces named "${name}":\n` +
      matches.map((s) => `  ${s.id}`).join("\n") +
      `\npass --create-space for a new one, or delete the duplicates first.`,
  );
}

// ---------------------------------------------------------------- scan

const SKIP_DIRS = new Set([".obsidian", ".trash", ".git", "Assets"]);

function* walk(dir, rel = "") {
  for (const name of readdirSync(dir).sort()) {
    if (SKIP_DIRS.has(name)) continue;
    const full = join(dir, name);
    if (statSync(full).isDirectory()) {
      yield* walk(full, rel ? `${rel}/${name}` : name);
    } else if (name.endsWith(".md")) {
      yield { full, rel, name };
    }
  }
}

// Strip [[target|alias]] → alias / [[target]] → target inside a value.
function stripWikilinks(s) {
  return s.replace(/\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|([^\]]+))?\]\]/g, (_, t, a) =>
    (a ?? t).trim(),
  );
}

function parseScalar(raw) {
  let v = raw.trim();
  if (v === "") return undefined;
  if (/^".*"$/.test(v) || /^'.*'$/.test(v)) v = v.slice(1, -1).trim();
  if (v === "") return undefined;
  if (v === "true") return true;
  if (v === "false") return false;
  if (/^-?\d+(\.\d+)?$/.test(v)) return Number(v);
  return stripWikilinks(v);
}

// Minimal YAML-ish frontmatter parser: `key: value` lines only, with
// [a, b] arrays, quoted strings, and [[wikilink]] values — all this
// vault uses. Tolerates the stray `url:: value` dataview syntax.
function parseFrontmatter(lines) {
  const fm = {};
  for (const line of lines) {
    const m = line.match(/^([A-Za-z_]+)::?\s*(.*)$/);
    if (!m) continue;
    const key = m[1];
    const raw = m[2].trim();
    if (raw.startsWith("[[")) {
      fm[key] = parseScalar(raw);
    } else if (/^\[.*\]$/.test(raw)) {
      const items = raw
        .slice(1, -1)
        .split(",")
        .map((s) => parseScalar(s))
        .filter((s) => s !== undefined);
      fm[key] = items;
    } else {
      const v = parseScalar(raw);
      if (v !== undefined) fm[key] = v;
    }
  }
  return fm;
}

const WIKILINK_RE = /(!?)\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|([^\]]+))?\]\]/g;

function parseNote(full, rel, name) {
  const text = readFileSync(full, "utf8");
  const lines = text.split("\n");
  let fm = {};
  let bodyStart = 0;
  let excalidraw = false;
  if (lines[0]?.trim() === "---") {
    const end = lines.findIndex((l, i) => i > 0 && l.trim() === "---");
    if (end > 0) {
      fm = parseFrontmatter(lines.slice(1, end));
      excalidraw = lines.slice(1, end).some((l) => l.startsWith("excalidraw-plugin:"));
      bodyStart = end + 1;
    }
  }
  let body = lines.slice(bodyStart).join("\n");

  // Inline dataview fields → created timestamp.
  const date = body.match(/\(date::\s*([0-9-]+)\s*\)/)?.[1];
  const time = body.match(/\(time::\s*([0-9:]+)\s*\)/)?.[1];
  const created = date ? (time ? `${date}T${time}` : date) : undefined;

  // Outgoing wikilinks (skip embeds) — collected before cleanup.
  const links = [];
  for (const m of body.matchAll(WIKILINK_RE)) {
    if (m[1] !== "!") links.push(m[2].trim());
  }

  return {
    title: name.replace(/\.md$/, ""),
    rel,
    fm,
    created,
    links,
    body: cleanBody(body, excalidraw),
  };
}

function cleanBody(body, excalidraw = false) {
  if (excalidraw) {
    // Keep only the human-readable text-elements section of an Excalidraw
    // doc; the canvas JSON under "# Drawing" is plugin data, not a note.
    body = body.split(/^# Drawing\s*$/m)[0];
    body = body.replace(/\s\^[A-Za-z0-9]+\s*$/gm, ""); // block-ref anchors
    body = body.replace(/^==⚠.*$/gm, "");
    body = "*(Excalidraw drawing — text elements only)*\n\n" + body;
  }
  // Dataview/templater fenced blocks carry no meaning outside Obsidian.
  body = body.replace(/```dataview(js)?\n[\s\S]*?```/g, "");
  let lines = body.split("\n");
  // Banner images and inline-field metadata lines.
  lines = lines.filter(
    (l) =>
      !l.includes("tp.web.random_picture") &&
      !/\((date|time|weather)::/.test(l),
  );
  body = lines.join("\n");
  // "Internal Links" footer (links are captured as properties instead).
  body = body.replace(/(\n-{3,}\s*)?\n#{1,6}\s*Internal Links[\s\S]*$/i, "\n");
  // Images are skipped entirely: vault-asset embeds (![[x.png]]), inline
  // markdown images, and giant base64 data-URI images alike.
  body = body.replace(/!\[\[[^\]]+\]\]/g, "");
  body = body.replace(/!\[[^\]]*\]\([^)]*\)/g, "");
  // Remaining wikilinks → plain text.
  body = stripWikilinks(body);
  // Editor blocks cap text at 64 KiB — truncate pathological lines.
  body = body
    .split("\n")
    .map((l) => (l.length > 60000 ? l.slice(0, 60000) + " …(truncated)" : l))
    .join("\n");
  // Drop a leading H1 — `any.name` already carries the title.
  body = body.replace(/^\s*# .*\n/, "");
  body = body.replace(/\n{3,}/g, "\n\n").trim();
  return dropEmptySections(body);
}

// Remove headings whose section has no content — the husks left behind
// where a dataview block or an unfilled template section used to be.
// Runs to fixpoint so a parent heading whose only child sections were
// dropped goes too.
function dropEmptySections(body) {
  const isBlank = (l) => {
    const t = l.trim();
    return t === "" || t === "---" || /^[-*]\s*(\[[ x]?\])?\s*$/.test(t);
  };
  let lines = body.split("\n");
  for (;;) {
    const out = [];
    let changed = false;
    for (let i = 0; i < lines.length; i++) {
      if (/^#{1,6}\s/.test(lines[i])) {
        let j = i + 1;
        let hasContent = false;
        while (j < lines.length && !/^#{1,6}\s/.test(lines[j])) {
          if (!isBlank(lines[j])) hasContent = true;
          j++;
        }
        if (!hasContent) {
          changed = true;
          i = j - 1; // skip heading + its blank filler
          continue;
        }
      }
      out.push(lines[i]);
    }
    lines = out;
    if (!changed) break;
  }
  return lines.join("\n").replace(/\n{3,}/g, "\n\n").trim();
}

// ---------------------------------------------------------------- types

// xKey → {name, description, props: {xKey: {name, kind}}}
const TYPE_DEFS = {
  zettel: {
    name: "Zettel",
    description: "Knowledge-base note imported from the Obsidian vault",
    props: {
      source: { name: "Source", kind: "string" },
      created: { name: "Created", kind: "string" },
      lastReviewed: { name: "Last reviewed", kind: "string" },
      aliases: { name: "Aliases", kind: "array" },
      tags: { name: "Tags", kind: "array" },
      links: { name: "Links", kind: "array" },
      topics: { name: "Topics", kind: "array" },
    },
  },
  topic: {
    name: "Topic",
    description: "Map-of-content hub page for one area of knowledge",
    props: {},
  },
  course: {
    name: "Course",
    description: "Online course or learning track",
    props: {
      url: { name: "URL", kind: "string" },
      progress: { name: "Progress %", kind: "number" },
      priority: { name: "Priority", kind: "number" },
    },
  },
  book: {
    name: "Book",
    description: "Book or whitepaper",
    props: {
      author: { name: "Author", kind: "string" },
      url: { name: "URL", kind: "string" },
      progress: { name: "Progress %", kind: "number" },
    },
  },
  article: {
    name: "Article",
    description: "Web article or blog post",
    props: {
      url: { name: "URL", kind: "string" },
      author: { name: "Author", kind: "string" },
      progress: { name: "Progress %", kind: "number" },
      priority: { name: "Priority", kind: "number" },
    },
  },
  person: {
    name: "Person",
    description: "Author, instructor, or other person of interest",
    props: {
      blog: { name: "Blog", kind: "string" },
      youtube: { name: "YouTube", kind: "string" },
      twitter: { name: "Twitter", kind: "string" },
      github: { name: "GitHub", kind: "string" },
      email: { name: "Email", kind: "string" },
      linkedin: { name: "LinkedIn", kind: "string" },
    },
  },
  problem: {
    name: "Practice Problem",
    description: "Coding-practice problem write-up",
    props: {
      platform: { name: "Platform", kind: "string" },
      url: { name: "URL", kind: "string" },
    },
  },
  daily: {
    name: "Daily Note",
    description: "Daily journal entry",
    props: {
      date: { name: "Date", kind: "string" },
      sleep: { name: "Sleep", kind: "string" },
      walked: { name: "Walked", kind: "boolean" },
      summary: { name: "Summary", kind: "string" },
      productivityScore: { name: "Productivity score", kind: "number" },
    },
  },
  weekly: {
    name: "Weekly Note",
    description: "Weekly review",
    props: {
      weekId: { name: "Week", kind: "string" },
      weekStarts: { name: "Starts", kind: "string" },
      weekEnds: { name: "Ends", kind: "string" },
      calendar: { name: "Calendar", kind: "string" },
      summary: { name: "Summary", kind: "string" },
      productivityScore: { name: "Productivity score", kind: "number" },
    },
  },
  monthly: {
    name: "Monthly Note",
    description: "Monthly review",
    props: {
      monthEnds: { name: "Month ends", kind: "string" },
      calendar: { name: "Calendar", kind: "string" },
      summary: { name: "Summary", kind: "string" },
      productivityScore: { name: "Productivity score", kind: "number" },
    },
  },
  trip: {
    name: "Trip",
    description: "Travel plan with dates and tickets",
    props: {
      destination: { name: "Destination", kind: "string" },
      starts: { name: "Starts", kind: "string" },
      ends: { name: "Ends", kind: "string" },
    },
  },
  ticket: {
    name: "Ticket",
    description: "Travel ticket / reservation",
    props: {
      trainNo: { name: "Train", kind: "string" },
      pnr: { name: "PNR", kind: "string" },
      pnrStatus: { name: "PNR status URL", kind: "string" },
      boardingFrom: { name: "From", kind: "string" },
      boardingTo: { name: "To", kind: "string" },
      reportingTime: { name: "Reporting", kind: "string" },
      departureTime: { name: "Departure", kind: "string" },
      arrivalTime: { name: "Arrival", kind: "string" },
      distance: { name: "Distance (km)", kind: "number" },
    },
  },
  event: {
    name: "Event",
    description: "Recurring calendar event",
    props: {
      date: { name: "Date", kind: "string" },
    },
  },
};

function percent(v) {
  if (v === undefined) return undefined;
  const n = parseFloat(String(v).replace("%", ""));
  return Number.isNaN(n) ? undefined : n;
}

// Per-note specific types + their property values, from template + folder.
function classify(note) {
  const { fm, rel, title } = note;
  const t = fm.template;
  const out = []; // [xKey, {propXKey: value}]
  const num = (v) => (typeof v === "number" ? v : percent(v));

  if (t === "moc") out.push(["topic", {}]);
  if (t === "course")
    out.push(["course", { url: fm.url, progress: percent(fm.progress), priority: num(fm.priority) }]);
  if (t === "book")
    out.push(["book", { author: fm.author, url: fm.url, progress: percent(fm.progress) }]);
  if (t === "article")
    out.push([
      "article",
      { url: fm.url, author: fm.author, progress: percent(fm.progress), priority: num(fm.priority) },
    ]);
  if (t === "people")
    out.push([
      "person",
      {
        blog: fm.blog,
        youtube: fm.youtube,
        twitter: fm.twitter,
        github: fm.github,
        email: fm.email,
        linkedin: fm.linkedin,
      },
    ]);
  if (t === "daily")
    out.push([
      "daily",
      {
        date: note.created?.slice(0, 10),
        sleep: fm.sleep,
        walked: fm.walked,
        summary: fm.summary,
        productivityScore: num(fm.productivity_score),
      },
    ]);
  if (t === "weekly")
    out.push([
      "weekly",
      {
        weekId: fm.week_id,
        weekStarts: String(fm.week_starts ?? ""),
        weekEnds: String(fm.week_ends ?? ""),
        calendar: fm.calendar,
        summary: fm.summary,
        productivityScore: num(fm.productivity_score),
      },
    ]);
  if (t === "monthly")
    out.push([
      "monthly",
      {
        monthEnds: String(fm.month_ends ?? ""),
        calendar: fm.calendar,
        summary: fm.summary,
        productivityScore: num(fm.productivity_score),
      },
    ]);
  if (t === "travel")
    out.push([
      "trip",
      {
        destination: Array.isArray(fm.aliases) ? fm.aliases[0] : title,
        starts: fm.starts,
        ends: fm.ends,
      },
    ]);
  if (t === "ticket")
    out.push([
      "ticket",
      {
        trainNo: fm.train_no,
        pnr: String(fm.pnr ?? ""),
        pnrStatus: fm.pnr_status,
        boardingFrom: fm.boarding_from,
        boardingTo: fm.boarding_to,
        reportingTime: fm.reporting_time,
        departureTime: fm.departure_time,
        arrivalTime: fm.arrival_time,
        distance: typeof fm.distance === "number" ? fm.distance : undefined,
      },
    ]);
  if (title.startsWith("LeetCode - "))
    out.push(["problem", { platform: "LeetCode", url: fm.source }]);
  if (rel.startsWith("Calendar/Events")) out.push(["event", { date: title }]);
  return out;
}

// Vault folder → KB folder path (nav tree).
function folderPath(rel) {
  if (rel === "") return "Notes";
  if (rel === "MOC") return "Topics";
  return rel;
}

// ---------------------------------------------------------------- main

async function main() {
  // ---- scan
  const notes = [];
  for (const f of walk(VAULT)) {
    const note = parseNote(f.full, f.rel, f.name);
    const isDashboard =
      (f.name === "Home.md" || f.name === "README.md") &&
      note.fm.template !== "travel";
    if (isDashboard) continue;
    notes.push(note);
  }

  // Title/alias index for wikilink resolution.
  const index = new Map(); // lowercased title/alias → canonical title
  const topicTitles = new Set();
  for (const n of notes) {
    index.set(n.title.toLowerCase(), n.title);
    const aliases = Array.isArray(n.fm.aliases) ? n.fm.aliases : [];
    for (const a of aliases) {
      const k = String(a).toLowerCase();
      if (!index.has(k)) index.set(k, n.title);
    }
    if (n.fm.template === "moc") topicTitles.add(n.title);
  }
  const resolve = (target) => index.get(target.toLowerCase());

  for (const n of notes) {
    const resolved = [...new Set(n.links.map(resolve).filter(Boolean))].filter(
      (t) => t !== n.title,
    );
    n.resolvedLinks = resolved;
    const topics = resolved.filter((t) => topicTitles.has(t));
    const parent = n.fm.parent ? resolve(String(n.fm.parent)) : undefined;
    if (parent && parent !== n.title && !topics.includes(parent)) topics.unshift(parent);
    n.topics = topics;
    n.specific = classify(n);
  }

  // Topic (MOC) pages: in the vault their sections were dataview queries
  // over backlinks ("FROM [[AWS]] WHERE template=course"). Materialize the
  // same rollup statically — we know every note's resolved links.
  const GROUPS = [
    ["📚 Books & Courses", new Set(["book", "course"])],
    ["📰 Articles", new Set(["article"])],
    ["👨🏻‍🏫 People", new Set(["people"])],
    ["🔗 Related Topics", new Set(["moc"])],
    ["🗒️ Notes", new Set()], // everything else
  ];
  for (const topic of notes.filter((n) => n.fm.template === "moc")) {
    const backlinks = notes.filter(
      (n) =>
        n !== topic &&
        (n.resolvedLinks.includes(topic.title) || n.topics.includes(topic.title)),
    );
    const sections = [];
    for (const [heading, templates] of GROUPS) {
      const members = backlinks.filter((n) =>
        templates.size
          ? templates.has(n.fm.template)
          : !GROUPS.some(([, t]) => t.has(n.fm.template)),
      );
      if (!members.length) continue;
      members.sort((a, b) => a.title.localeCompare(b.title));
      sections.push(
        `## ${heading}\n\n` + members.map((n) => `- ${n.title}`).join("\n"),
      );
    }
    topic.body = [topic.body, ...sections].filter(Boolean).join("\n\n");
  }

  console.log(`scanned ${notes.length} notes from ${VAULT}`);
  if (DRY) {
    const byType = {};
    for (const n of notes)
      for (const [k] of n.specific.length ? n.specific : [["(zettel only)"]])
        byType[k] = (byType[k] ?? 0) + 1;
    console.log("objects per specific type:", byType);
    for (const n of notes.slice(0, 8))
      console.log(
        `- ${folderPath(n.rel)}/${n.title} types=[zettel${n.specific.map(([k]) => "," + k).join("")}] links=${n.resolvedLinks.length} topics=[${n.topics}]`,
      );
    return;
  }

  // ---- space
  if (CREATE_SPACE) {
    const sp = await api("POST", "/v1/spaces", {
      name: CREATE_SPACE,
      description: "Imported Obsidian knowledge base",
    });
    SPACE = sp.id;
    console.log(`created space ${SPACE} (${CREATE_SPACE})`);
  } else {
    SPACE = await resolveSpaceByName(SPACE_NAME);
    console.log(`resolved space "${SPACE_NAME}" → ${SPACE}`);
  }
  const S = `/v1/spaces/${encodeURIComponent(SPACE)}`;

  // ---- types
  const typeIds = {}; // xKey → typeId
  const propIds = {}; // xKey → {propXKey → propId}
  for (const [xKey, def] of Object.entries(TYPE_DEFS)) {
    const { typeId } = await api("POST", `${S}/types`, {
      name: def.name,
      description: def.description,
      xKey,
    });
    typeIds[xKey] = typeId;
    propIds[xKey] = {};
    for (const [pKey, p] of Object.entries(def.props)) {
      const { propId } = await api("POST", `${S}/types/${typeId}/properties`, {
        name: p.name,
        kind: p.kind,
        xKey: pKey,
      });
      propIds[xKey][pKey] = propId;
    }
    console.log(`type ${def.name} (${xKey}) → ${typeId}`);
  }

  // ---- folders
  const ROOT_ORDER = ["Notes", "Topics", "Courses", "Books", "Articles", "People", "Calendar", "Travel", "Work"];
  const allPaths = new Set(notes.map((n) => folderPath(n.rel)));
  // Ensure ancestors exist, then order: ROOT_ORDER first, depth-first.
  const expanded = new Set();
  for (const p of allPaths) {
    const segs = p.split("/");
    for (let i = 1; i <= segs.length; i++) expanded.add(segs.slice(0, i).join("/"));
  }
  const ordered = [...expanded].sort((a, b) => {
    const ra = ROOT_ORDER.indexOf(a.split("/")[0]);
    const rb = ROOT_ORDER.indexOf(b.split("/")[0]);
    if (ra !== rb) return (ra === -1 ? 99 : ra) - (rb === -1 ? 99 : rb);
    return a.localeCompare(b);
  });
  const folderIds = new Map(); // path → objectId
  for (const p of ordered) {
    const segs = p.split("/");
    const parentId = segs.length > 1 ? folderIds.get(segs.slice(0, -1).join("/")) : "";
    const { objectId } = await api("POST", `${S}/objects`, {
      types: [],
      initialProperties: { any: { name: segs.at(-1) } },
      nav: { type: 2, parentId },
    });
    folderIds.set(p, objectId);
  }
  console.log(`created ${folderIds.size} folders`);

  // ---- objects
  let created = 0;
  for (const n of notes) {
    const zettelVals = {};
    const zp = propIds.zettel;
    const setIf = (key, v) => {
      if (v !== undefined && v !== "" && !(Array.isArray(v) && v.length === 0))
        zettelVals[zp[key]] = v;
    };
    setIf("source", n.fm.source);
    setIf("created", n.created);
    setIf("lastReviewed", n.fm.last_reviewed);
    setIf(
      "aliases",
      Array.isArray(n.fm.aliases) ? n.fm.aliases.map(String) : undefined,
    );
    const tags = Array.isArray(n.fm.tags)
      ? n.fm.tags.map(String)
      : typeof n.fm.tags === "string"
        ? n.fm.tags.split(",").map((s) => s.trim()).filter(Boolean)
        : undefined;
    setIf("tags", tags);
    setIf("links", n.resolvedLinks);
    setIf("topics", n.topics);

    const types = [typeIds.zettel];
    const initialProperties = {
      any: { name: n.title },
      [typeIds.zettel]: zettelVals,
    };
    for (const [xKey, vals] of n.specific) {
      types.push(typeIds[xKey]);
      const out = {};
      for (const [pKey, v] of Object.entries(vals)) {
        if (v !== undefined && v !== "") out[propIds[xKey][pKey]] = v;
      }
      initialProperties[typeIds[xKey]] = out;
    }

    const { objectId } = await api("POST", `${S}/objects`, {
      types,
      initialProperties,
      nav: { parentId: folderIds.get(folderPath(n.rel)) },
    });
    if (n.body) {
      await api("PUT", `${S}/objects/${objectId}/editor/markdown`, {
        content: n.body,
      });
    }
    created++;
    if (created % 25 === 0) console.log(`  ${created}/${notes.length} objects…`);
  }
  console.log(`done: ${created} objects in space ${SPACE}`);
}

main().catch((e) => {
  console.error(e.message ?? e);
  process.exit(1);
});
