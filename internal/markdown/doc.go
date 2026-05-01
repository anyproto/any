// Package markdown ships the `md` type — a markdown document stored
// as a stream of block records, one record per CommonMark-ish block.
// Record ids are lexids: they sort into the document order and allow
// inserting new blocks between existing ones without renumbering.
//
// The package contains:
//
//   - NewType: a handler.Type to wire into config.Config.Types so the
//     SDK accepts writes on the `md_blocks` dataset.
//   - Set / List / Get: helpers that read existing blocks via
//     space.Query, run the splitter + diff + lexid allocator, and
//     emit per-record ops so unchanged blocks keep their lexids.
//
// All splitting and diffing is caller-side: the handler itself does
// no per-block work and stores only `{id: lexid, text: string}`.
// The SDK only sees plain record reads / writes; the diff machinery
// can evolve here without bumping the on-the-wire DataVersion.
package markdown
