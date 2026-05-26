// memory-bootstrap@v1
// Bootstrap program for the structured memory system.
// Creates memory types and their properties via anyHelper.
// Safe to re-run — createType skips types that already exist.

import { createClient } from "anyHelper@v1";

// ============================================================
// SCHEMA DEFINITION
// ============================================================

var SHARED_PROPERTIES = [
  {key: 'mem_salience', name: 'Salience', format: 'number'},
  {key: 'mem_access_count', name: 'Access Count', format: 'number'},
  {key: 'mem_confidence', name: 'Confidence', format: 'number'},
  {key: 'mem_importance', name: 'Importance', format: 'number'},
  {key: 'mem_entities', name: 'Entities', format: 'text'},
  {key: 'mem_keywords', name: 'Keywords', format: 'text'},
  {key: 'mem_source_turn', name: 'Source Turn', format: 'text'},
  {key: 'mem_valid_from', name: 'Valid From', format: 'text'},
  {key: 'mem_valid_until', name: 'Valid Until', format: 'text'},
  {key: 'mem_reflected', name: 'Reflected', format: 'checkbox'},
  {key: 'mem_archived_date', name: 'Archived Date', format: 'text'},
  {key: 'mem_vector', name: 'Vector', format: 'text'}
];

var TYPE_PROPERTIES = [
  {key: 'claim_key', name: 'Claim Key', format: 'text'},
  {key: 'claim_value', name: 'Value', format: 'text'},
  {key: 'mem_superseded', name: 'Superseded', format: 'checkbox'},
  {key: 'mem_reflect_checked', name: 'Reflect Checked', format: 'text'},
  {key: 'episode_summary', name: 'Summary', format: 'text'},
  {key: 'corrective_action', name: 'Corrective Action', format: 'text'},
  {key: 'preference_key', name: 'Preference Key', format: 'text'},
  {key: 'preference_value', name: 'Preference Value', format: 'text'},
  {key: 'mem_stable', name: 'Stable', format: 'checkbox'},
  {key: 'decision_rationale', name: 'decision_rationale', format: 'text'},
  {key: 'decision_alternatives', name: 'decision_alternatives', format: 'text'},
  {key: 'task_next_action', name: 'task_next_action', format: 'text'},
  {key: 'task_blocker', name: 'task_blocker', format: 'text'},
  {key: 'task_resolved_by', name: 'task_resolved_by', format: 'text'},
  {key: 'insight_abstraction', name: 'Insight Abstraction', format: 'text'},
  {key: 'insight_invalidated', name: 'Insight Invalidated', format: 'checkbox'},
  {key: 'entity_canonical', name: 'Canonical Name', format: 'text'},
  {key: 'entity_raw_label', name: 'Raw Label', format: 'text'},
  {key: 'entity_type', name: 'Entity Type', format: 'text'},
  {key: 'entity_aliases', name: 'Aliases', format: 'text'},
  {key: 'edge_type', name: 'Edge Type', format: 'text'},
  {key: 'edge_strength', name: 'Strength', format: 'number'},
  {key: 'edge_source_method', name: 'Source Method', format: 'text'},
  {key: 'edge_created', name: 'Created At', format: 'text'},
  {key: 'evidence_source', name: 'evidence_source', format: 'text'},
  {key: 'evidence_type', name: 'evidence_type', format: 'text'},
  {key: 'evidence_created', name: 'evidence_created', format: 'text'},
  {key: 'scope_dimension', name: 'scope_dimension', format: 'text'},
  {key: 'scope_value', name: 'scope_value', format: 'text'},
  {key: 'scope_specificity', name: 'scope_specificity', format: 'number'}
];

var ALL_PROP_DEFS = SHARED_PROPERTIES.concat(TYPE_PROPERTIES);

var TYPES = [
  {
    name: 'Memory Claim',
    shared: true,
    props: ['claim_key', 'claim_value', 'mem_superseded', 'mem_reflect_checked']
  },
  {
    name: 'Memory Episode',
    shared: true,
    props: ['episode_summary']
  },
  {
    name: 'Memory Lesson',
    shared: true,
    props: ['corrective_action']
  },
  {
    name: 'Memory Preference',
    shared: true,
    props: ['preference_key', 'preference_value', 'mem_stable']
  },
  {
    name: 'Memory Decision',
    shared: true,
    props: ['decision_rationale', 'decision_alternatives']
  },
  {
    name: 'Memory Task',
    shared: true,
    props: ['task_next_action', 'task_blocker', 'task_resolved_by']
  },
  {
    name: 'Memory Insight',
    shared: true,
    props: ['insight_abstraction', 'insight_invalidated']
  },
  {
    name: 'Memory Entity',
    shared: false,
    props: ['entity_canonical', 'entity_raw_label', 'entity_type', 'entity_aliases']
  },
  {
    name: 'Memory Edge',
    shared: false,
    props: ['edge_type', 'edge_strength', 'edge_source_method', 'edge_created']
  },
  {
    name: 'Memory Evidence',
    shared: false,
    props: ['evidence_source', 'evidence_type', 'evidence_created']
  },
  {
    name: 'Memory Scope',
    shared: false,
    props: ['scope_dimension', 'scope_value', 'scope_specificity']
  }
];

// ============================================================
// BOOTSTRAP
// ============================================================

function resolveProps(typeDef) {
  var props = [];
  if (typeDef.shared) {
    for (var i = 0; i < SHARED_PROPERTIES.length; i++) {
      props.push(SHARED_PROPERTIES[i]);
    }
  }
  for (var j = 0; j < typeDef.props.length; j++) {
    var pk = typeDef.props[j];
    for (var k = 0; k < ALL_PROP_DEFS.length; k++) {
      if (ALL_PROP_DEFS[k].key === pk) {
        props.push(ALL_PROP_DEFS[k]);
        break;
      }
    }
  }
  return props;
}

export function main(args) {
  if (!args) args = {};
  var spaceId = args.spaceId || (typeof env !== 'undefined' ? env.ANYTYPE_SPACE_ID : null);
  var apiBaseUrl = args.apiBaseUrl || (typeof env !== 'undefined' ? env.ANYTYPE_API_URL : null);
  if (!spaceId || !apiBaseUrl) {
    return "Error: spaceId and apiBaseUrl required";
  }

  var client = createClient({ apiBaseUrl: apiBaseUrl, spaceId: spaceId });

  var report = [];
  var created = 0;
  var skipped = 0;
  var errors = [];

  report.push('# Memory Bootstrap Report');
  report.push('');

  for (var i = 0; i < TYPES.length; i++) {
    var typeDef = TYPES[i];
    var props = resolveProps(typeDef);
    var result = client.createType({ name: typeDef.name, properties: props });
    if (!result || !result.ok) {
      errors.push(typeDef.name + ': ' + (result ? result.error : 'unknown'));
    } else if (result.created) {
      report.push('- Created type: ' + typeDef.name + ' (' + props.length + ' properties)');
      created++;
    } else {
      skipped++;
    }
  }

  report.push('');
  report.push('## Summary');
  report.push('- Created: ' + created + ' types');
  report.push('- Skipped: ' + skipped + ' types (already exist)');
  report.push('- Errors: ' + errors.length);

  if (errors.length > 0) {
    report.push('');
    report.push('## Errors');
    for (var ei = 0; ei < errors.length; ei++) {
      report.push('- ' + errors[ei]);
    }
  }

  if (created === 0 && errors.length === 0) {
    report.push('');
    report.push('Space is fully bootstrapped. No changes needed.');
  }

  return report.join('\n');
}
