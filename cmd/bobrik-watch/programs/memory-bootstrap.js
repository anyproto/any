// memory-bootstrap@v1
// Bootstrap program for the structured memory system.
// Checks what types, properties, and tags exist in the space,
// creates anything missing, and verifies the schema is complete.
//
// Usage: Run as Sobek program. Returns a report of what was created.
// Safe to re-run — skips anything that already exists.
//
// This file is the single source of truth for the memory schema.
// When adding new memory types or properties, update SCHEMA below.

// ============================================================
// SCHEMA DEFINITION — single source of truth
// ============================================================

var SCHEMA = {

  // --- Shared properties (used across multiple memory types) ---
  sharedProperties: [
    {key: 'mem_salience', name: 'Salience', format: 'number'},
    {key: 'mem_access_count', name: 'Access Count', format: 'number'},
    {key: 'mem_confidence', name: 'Confidence', format: 'number'},
    {key: 'mem_importance', name: 'Importance', format: 'number'},
    {key: 'mem_entities', name: 'Entities', format: 'objects'},
    {key: 'mem_keywords', name: 'Keywords', format: 'text'},
    {key: 'mem_source_turn', name: 'Source Turn', format: 'text'},
    {key: 'mem_valid_from', name: 'Valid From', format: 'text'},
    {key: 'mem_valid_until', name: 'Valid Until', format: 'text'},
    {key: 'mem_scope', name: 'Scope', format: 'objects'},
    {key: 'mem_reflected', name: 'Reflected', format: 'checkbox'},
    {key: 'mem_archived_date', name: 'Archived Date', format: 'text'},
    {key: 'mem_vector', name: 'Vector', format: 'text'}
  ],

  // --- Type-specific properties ---
  typeProperties: [
    // claim
    {key: 'claim_key', name: 'Claim Key', format: 'text'},
    {key: 'claim_value', name: 'Value', format: 'text'},
    {key: 'mem_superseded', name: 'Superseded', format: 'checkbox'},
    {key: 'mem_reflect_checked', name: 'Reflect Checked', format: 'text'},
    // episode
    {key: 'episode_summary', name: 'Summary', format: 'text'},
    {key: 'mem_outcome', name: 'Outcome', format: 'select'},
    // lesson
    {key: 'corrective_action', name: 'Corrective Action', format: 'text'},
    {key: 'mem_severity', name: 'Severity', format: 'select'},
    // preference
    {key: 'preference_key', name: 'Preference Key', format: 'text'},
    {key: 'preference_value', name: 'Preference Value', format: 'text'},
    {key: 'mem_stable', name: 'Stable', format: 'checkbox'},
    // decision
    {key: 'decision_rationale', name: 'decision_rationale', format: 'text'},
    {key: 'decision_alternatives', name: 'decision_alternatives', format: 'text'},
    {key: 'decision_status', name: 'decision_status', format: 'select'},
    // taskstate
    {key: 'task_next_action', name: 'task_next_action', format: 'text'},
    {key: 'task_blocker', name: 'task_blocker', format: 'text'},
    {key: 'task_owner', name: 'task_owner', format: 'objects'},
    {key: 'task_status', name: 'task_status', format: 'select'},
    {key: 'task_resolved_by', name: 'task_resolved_by', format: 'text'},
    // insight
    {key: 'insight_abstraction', name: 'Insight Abstraction', format: 'select'},
    {key: 'insight_invalidated', name: 'Insight Invalidated', format: 'checkbox'},
    // entity
    {key: 'entity_canonical', name: 'Canonical Name', format: 'text'},
    {key: 'entity_raw_label', name: 'Raw Label', format: 'text'},
    {key: 'entity_type', name: 'Entity Type', format: 'select'},
    {key: 'entity_aliases', name: 'Aliases', format: 'text'},
    // edge
    {key: 'edge_from', name: 'From', format: 'objects'},
    {key: 'edge_to', name: 'To', format: 'objects'},
    {key: 'edge_type', name: 'Edge Type', format: 'select'},
    {key: 'edge_strength', name: 'Strength', format: 'number'},
    {key: 'edge_source_method', name: 'Source Method', format: 'select'},
    {key: 'edge_created', name: 'Created At', format: 'text'},
    // evidence
    {key: 'evidence_source', name: 'evidence_source', format: 'text'},
    {key: 'evidence_type', name: 'evidence_type', format: 'select'},
    {key: 'evidence_created', name: 'evidence_created', format: 'text'},
    // scope
    {key: 'scope_dimension', name: 'scope_dimension', format: 'select'},
    {key: 'scope_value', name: 'scope_value', format: 'text'},
    {key: 'scope_specificity', name: 'scope_specificity', format: 'number'},
    {key: 'scope_parent', name: 'scope_parent', format: 'objects'}
  ],

  // --- Memory types ---
  // 'shared' means all sharedProperties are included automatically
  // 'props' lists additional type-specific property keys
  types: [
    {
      key: 'mem_claim', name: 'Memory Claim', plural: 'Memory Claims',
      icon: '📌', layout: 'basic', shared: true,
      props: ['claim_key', 'claim_value', 'mem_superseded', 'mem_reflect_checked']
    },
    {
      key: 'mem_episode', name: 'Memory Episode', plural: 'Memory Episodes',
      icon: '📖', layout: 'basic', shared: true,
      props: ['episode_summary', 'mem_outcome']
    },
    {
      key: 'mem_lesson', name: 'Memory Lesson', plural: 'Memory Lessons',
      icon: '⚠️', layout: 'basic', shared: true,
      props: ['corrective_action', 'mem_severity']
    },
    {
      key: 'mem_preference', name: 'Memory Preference', plural: 'Memory Preferences',
      icon: '⭐', layout: 'basic', shared: true,
      props: ['preference_key', 'preference_value', 'mem_stable']
    },
    {
      key: 'mem_decision', name: 'mem_decision', plural: 'mem_decisions',
      icon: '🔷', layout: 'basic', shared: true,
      props: ['decision_rationale', 'decision_alternatives', 'decision_status']
    },
    {
      key: 'mem_taskstate', name: 'mem_taskstate', plural: 'mem_taskstates',
      icon: '✅', layout: 'action', shared: true,
      props: ['task_next_action', 'task_blocker', 'task_owner', 'task_status', 'task_resolved_by']
    },
    {
      key: 'mem_insight', name: 'mem_insight', plural: 'mem_insights',
      icon: '💡', layout: 'basic', shared: true,
      props: ['insight_abstraction', 'insight_invalidated']
    },
    {
      key: 'mem_entity', name: 'Memory Entity', plural: 'Memory Entities',
      icon: '🔗', layout: 'basic', shared: false,
      props: ['entity_canonical', 'entity_raw_label', 'entity_type', 'entity_aliases']
    },
    {
      key: 'mem_edge', name: 'Memory Edge', plural: 'Memory Edges',
      icon: '➡️', layout: 'basic', shared: false,
      props: ['edge_from', 'edge_to', 'edge_type', 'edge_strength', 'edge_source_method', 'edge_created']
    },
    {
      key: 'mem_evidence', name: 'mem_evidence', plural: 'mem_evidences',
      icon: '📎', layout: 'basic', shared: false,
      props: ['evidence_source', 'evidence_type', 'evidence_created']
    },
    {
      key: 'mem_scope', name: 'mem_scope', plural: 'mem_scopes',
      icon: '🎯', layout: 'basic', shared: false,
      props: ['scope_dimension', 'scope_value', 'scope_specificity', 'scope_parent']
    }
  ],

  // --- Select tags (created on select-format properties) ---
  tags: [
    {property: 'mem_outcome', values: ['success', 'failure', 'partial']},
    {property: 'mem_severity', values: ['critical', 'important', 'minor']},
    {property: 'decision_status', values: ['active', 'superseded', 'reversed']},
    {property: 'task_status', values: ['task_open', 'blocked', 'resolved']},
    {property: 'insight_abstraction', values: ['pattern', 'generalization', 'mental_model']},
    {property: 'entity_type', values: ['concept']},
    {property: 'evidence_type', values: ['transcript']},
    {property: 'edge_type', values: ['related_to', 'caused_by', 'led_to', 'contradicts', 'supersedes', 'supported_by']},
    {property: 'edge_source_method', values: ['retain_extraction', 'llm_detected', 'llm_reflect', 'entity_cooccurrence', 'temporal_sequence', 'contradiction_detected', 'reflect_synthesis']},
    {property: 'scope_dimension', values: ['dim_project', 'dim_language', 'environment', 'api_version', 'plan_tier', 'user', 'platform']}
  ]
};

// ============================================================
// BOOTSTRAP ENGINE
// ============================================================

function makeHeaders(apiKey) {
  return {
    'Content-Type': 'application/json',
    'Authorization': 'Bearer ' + apiKey
  };
}

// Fetch all existing types in the space
function fetchExistingTypes(url, sid, h) {
  var r = fetch(url + '/v1/spaces/' + sid + '/types', {
    method: 'GET', headers: h
  });
  if (!r.ok || !r.body || !r.body.data) return {};
  var map = {};
  for (var i = 0; i < r.body.data.length; i++) {
    map[r.body.data[i].key] = r.body.data[i];
  }
  return map;
}

// Fetch all existing properties in the space (paginated)
function fetchExistingProperties(url, sid, h) {
  var map = {};
  var offset = 0;
  var hasMore = true;
  while (hasMore) {
    var r = fetch(url + '/v1/spaces/' + sid + '/properties?limit=100&offset=' + offset, {
      method: 'GET', headers: h
    });
    if (!r.ok || !r.body || !r.body.data) break;
    for (var i = 0; i < r.body.data.length; i++) {
      map[r.body.data[i].key] = r.body.data[i];
    }
    hasMore = r.body.pagination && r.body.pagination.has_more;
    offset = offset + 100;
  }
  return map;
}

// Fetch all existing tags for a property
function fetchExistingTags(url, sid, h, propId) {
  var r = fetch(url + '/v1/spaces/' + sid + '/properties/' + propId + '/tags', {
    method: 'GET', headers: h
  });
  if (!r.ok || !r.body || !r.body.data) return {};
  var map = {};
  for (var i = 0; i < r.body.data.length; i++) {
    var tag = r.body.data[i];
    map[tag.key || tag.name] = tag;
  }
  return map;
}

// Create a property
function createProperty(url, sid, h, prop) {
  var body = {key: prop.key, name: prop.name, format: prop.format};
  var r = fetch(url + '/v1/spaces/' + sid + '/properties', {
    method: 'POST', headers: h,
    body: JSON.stringify(body)
  });
  if (r.ok && r.body && r.body.property) {
    return {ok: true, id: r.body.property.id};
  }
  return {ok: false, error: r.body ? JSON.stringify(r.body).substring(0, 200) : 'unknown'};
}

// Create a type with properties
function createType(url, sid, h, typeDef, allPropDefs) {
  // Build property list for this type
  var props = [];
  for (var i = 0; i < typeDef.props.length; i++) {
    var pk = typeDef.props[i];
    // Find the property definition
    var found = null;
    for (var j = 0; j < allPropDefs.length; j++) {
      if (allPropDefs[j].key === pk) { found = allPropDefs[j]; break; }
    }
    if (found) {
      props.push({key: found.key, name: found.name, format: found.format});
    }
  }

  // If shared, prepend shared properties
  if (typeDef.shared) {
    var sharedProps = [];
    for (var s = 0; s < SCHEMA.sharedProperties.length; s++) {
      var sp = SCHEMA.sharedProperties[s];
      sharedProps.push({key: sp.key, name: sp.name, format: sp.format});
    }
    props = sharedProps.concat(props);
  }

  var body = {
    key: typeDef.key,
    name: typeDef.name,
    plural_name: typeDef.plural,
    icon: {format: 'emoji', emoji: typeDef.icon},
    layout: typeDef.layout,
    properties: props
  };

  var r = fetch(url + '/v1/spaces/' + sid + '/types', {
    method: 'POST', headers: h,
    body: JSON.stringify(body)
  });
  if (r.ok && r.body && r.body.type) {
    return {ok: true, id: r.body.type.id};
  }
  return {ok: false, error: r.body ? JSON.stringify(r.body).substring(0, 200) : 'unknown'};
}

// Create a tag on a property
function createTag(url, sid, h, propId, tagKey) {
  var body = {key: tagKey, name: tagKey};
  var r = fetch(url + '/v1/spaces/' + sid + '/properties/' + propId + '/tags', {
    method: 'POST', headers: h,
    body: JSON.stringify(body)
  });
  if (r.ok) return {ok: true};
  return {ok: false, error: r.body ? JSON.stringify(r.body).substring(0, 200) : 'unknown'};
}

// ============================================================
// MAIN BOOTSTRAP
// ============================================================

export function main(args) {
  // Diagnostic: dump all arg keys so we can see what runProgram actually passes
  var argKeys = [];
  if (args) { for (var k in args) { if (args.hasOwnProperty(k)) { argKeys.push(k + '=' + typeof args[k]); } } }

  // Accept config from: runProgram auto-merge | explicit args | env global
  // Try every plausible field name for each credential
  var cfg = (args && args.env) ? args.env : (typeof env !== 'undefined' ? env : {});
  var url = (args && args.apiBaseUrl) || (args && args.apiUrl) || (args && args.url) || cfg.ANYTYPE_API_URL;
  var sid = (args && args.spaceId) || (args && args.space_id) || cfg.ANYTYPE_SPACE_ID;
  var apiKey = (args && args.apiKey) || (args && args.api_key) || (args && args.key) || (args && args.token) || (args && args.apiToken) || cfg.ANYTYPE_API_KEY;
  if (!url || !sid || !apiKey) {
    throw new Error('Missing config. args keys: [' + argKeys.join(', ') + ']. Got: url=' + !!url + ' sid=' + !!sid + ' key=' + !!apiKey);
  }
  var h = makeHeaders(apiKey);

  var report = [];
  var created = {properties: 0, types: 0, tags: 0};
  var skipped = {properties: 0, types: 0, tags: 0};
  var errors = [];

  report.push('# Memory Bootstrap Report');
  report.push('');

  // --- Phase 1: Properties ---
  report.push('## Phase 1: Properties');
  var existingProps = fetchExistingProperties(url, sid, h);
  var allPropDefs = SCHEMA.sharedProperties.concat(SCHEMA.typeProperties);

  for (var pi = 0; pi < allPropDefs.length; pi++) {
    var prop = allPropDefs[pi];
    if (existingProps[prop.key]) {
      skipped.properties++;
    } else {
      var pr = createProperty(url, sid, h, prop);
      if (pr.ok) {
        report.push('- Created property: ' + prop.key + ' (' + prop.format + ')');
        created.properties++;
        // Add to existing map for tag phase
        existingProps[prop.key] = {id: pr.id, key: prop.key, format: prop.format};
      } else {
        errors.push('Property ' + prop.key + ': ' + pr.error);
      }
    }
  }
  report.push('- Properties: ' + created.properties + ' created, ' + skipped.properties + ' existing');
  report.push('');

  // --- Phase 2: Types ---
  report.push('## Phase 2: Types');
  var existingTypes = fetchExistingTypes(url, sid, h);

  for (var ti = 0; ti < SCHEMA.types.length; ti++) {
    var typeDef = SCHEMA.types[ti];
    if (existingTypes[typeDef.key]) {
      skipped.types++;
    } else {
      var tr = createType(url, sid, h, typeDef, allPropDefs);
      if (tr.ok) {
        report.push('- Created type: ' + typeDef.key + ' (' + typeDef.icon + ')');
        created.types++;
      } else {
        errors.push('Type ' + typeDef.key + ': ' + tr.error);
      }
    }
  }
  report.push('- Types: ' + created.types + ' created, ' + skipped.types + ' existing');
  report.push('');

  // --- Phase 3: Select Tags ---
  report.push('## Phase 3: Select Tags');

  for (var si = 0; si < SCHEMA.tags.length; si++) {
    var tagDef = SCHEMA.tags[si];
    var propInfo = existingProps[tagDef.property];
    if (!propInfo || !propInfo.id) {
      errors.push('Tag property ' + tagDef.property + ' not found — cannot create tags');
      continue;
    }
    var existingTags = fetchExistingTags(url, sid, h, propInfo.id);
    for (var tv = 0; tv < tagDef.values.length; tv++) {
      var tagVal = tagDef.values[tv];
      if (existingTags[tagVal]) {
        skipped.tags++;
      } else {
        var tgr = createTag(url, sid, h, propInfo.id, tagVal);
        if (tgr.ok) {
          report.push('- Created tag: ' + tagDef.property + ' -> ' + tagVal);
          created.tags++;
        } else {
          errors.push('Tag ' + tagDef.property + '/' + tagVal + ': ' + tgr.error);
        }
      }
    }
  }
  report.push('- Tags: ' + created.tags + ' created, ' + skipped.tags + ' existing');
  report.push('');

  // --- Phase 4: Verification ---
  report.push('## Phase 4: Verification');
  var verifyProps = fetchExistingProperties(url, sid, h);
  var verifyTypes = fetchExistingTypes(url, sid, h);
  var missingProps = [];
  var missingTypes = [];

  for (var vp = 0; vp < allPropDefs.length; vp++) {
    if (!verifyProps[allPropDefs[vp].key]) missingProps.push(allPropDefs[vp].key);
  }
  for (var vt = 0; vt < SCHEMA.types.length; vt++) {
    if (!verifyTypes[SCHEMA.types[vt].key]) missingTypes.push(SCHEMA.types[vt].key);
  }

  if (missingProps.length === 0 && missingTypes.length === 0) {
    report.push('- All ' + allPropDefs.length + ' properties verified');
    report.push('- All ' + SCHEMA.types.length + ' types verified');
    report.push('- Schema is complete');
  } else {
    if (missingProps.length > 0) report.push('- MISSING properties: ' + missingProps.join(', '));
    if (missingTypes.length > 0) report.push('- MISSING types: ' + missingTypes.join(', '));
  }
  report.push('');

  // --- Errors ---
  if (errors.length > 0) {
    report.push('## Errors');
    for (var ei = 0; ei < errors.length; ei++) {
      report.push('- ' + errors[ei]);
    }
    report.push('');
  }

  // --- Summary ---
  report.push('## Summary');
  report.push('- Created: ' + created.properties + ' properties, ' + created.types + ' types, ' + created.tags + ' tags');
  report.push('- Skipped: ' + skipped.properties + ' properties, ' + skipped.types + ' types, ' + skipped.tags + ' tags');
  report.push('- Errors: ' + errors.length);

  var totalCreated = created.properties + created.types + created.tags;
  if (totalCreated === 0 && errors.length === 0) {
    report.push('');
    report.push('Space is fully bootstrapped. No changes needed.');
  }

  return report.join('\n');
}
