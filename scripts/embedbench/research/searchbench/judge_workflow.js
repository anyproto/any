export const meta = {
  name: 'searchbench-judge',
  description: 'Blind relevance judging of rank-1 disagreement records: 2 Haiku votes per pair, Sonnet settles splits',
  phases: [
    { title: 'Vote', detail: 'two independent Haiku judges per batch', model: 'haiku' },
    { title: 'Tiebreak', detail: 'Sonnet grades pairs where the votes split', model: 'sonnet' },
  ],
}

const GRADES = {
  type: 'object',
  properties: {
    grades: {
      type: 'array',
      items: {
        type: 'object',
        properties: { pid: { type: 'string' }, grade: { type: 'integer', minimum: 0, maximum: 3 } },
        required: ['pid', 'grade'],
      },
    },
  },
  required: ['grades'],
}

const RUBRIC = `Grade how well the RECORD satisfies the searcher's NEED. The NEED explains what the person who typed the QUERY is looking for.
3 = the record is what they are looking for, or fully satisfies the need
2 = substantially helps: it covers a main part of the need
1 = same general topic, but does not satisfy the need
0 = unrelated
Judge only from the text shown. Don't guess at content that isn't there, and don't reward word overlap with the query by itself. You are not told how the record was found, and that doesn't matter.`

const batches = args.batches
const results = await pipeline(batches,
  async (path, _, i) => {
    const votes = await parallel([0, 1].map(v => () => agent(
      `You are relevance judge #${v + 1} for a retrieval evaluation. Read the JSON Lines file ${path}. Each line has pid, query, need and record. Read only this file, and make no network calls.\n\n${RUBRIC}\n\nReturn one grade for EVERY pid in the file.`,
      { label: `vote ${i}.${v + 1}`, phase: 'Vote', model: 'haiku', schema: GRADES })))
    return votes
  },
  async (votes, path, i) => {
    const [a, b] = (votes || []).map(x => x ? Object.fromEntries(x.grades.map(g => [g.pid, g.grade])) : {})
    const pids = Object.keys(a).filter(p => p in b)
    const split = pids.filter(p => Math.abs(a[p] - b[p]) >= 2 || ((a[p] >= 2) !== (b[p] >= 2)))
    let tb = {}
    if (split.length) {
      const r = await agent(
        `You are the senior relevance judge for a retrieval evaluation. Read the JSON Lines file ${path}. Each line has pid, query, need and record. Read only this file, and make no network calls. Grade ONLY these pids: ${split.join(', ')}.\n\n${RUBRIC}`,
        { label: `tiebreak ${i}`, phase: 'Tiebreak', model: 'sonnet', schema: GRADES })
      if (r) tb = Object.fromEntries(r.grades.map(g => [g.pid, g.grade]))
    }
    return { path, a, b, tb, split: split.length, missing: [...new Set([...Object.keys(a), ...Object.keys(b)])].filter(p => !(p in a) || !(p in b)).length }
  })
return results
