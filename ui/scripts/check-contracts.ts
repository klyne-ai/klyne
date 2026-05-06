#!/usr/bin/env tsx
/**
 * check-contracts.ts — CI guard against TS↔Go contract drift (mitigates risk R4).
 *
 * Reads the JSON produced by `go run ./tools/dump-contracts` (W0's dump tool)
 * and asserts that every Go struct in the dump has a corresponding TypeScript
 * interface in ui/src/lib/types.ts with all fields present (matched by JSON tag
 * name, which equals the TS field name).
 *
 * Usage:
 *   npx tsx scripts/check-contracts.ts
 *   npx tsx scripts/check-contracts.ts --dump /path/to/contract-dump.json
 *
 * Exit codes:
 *   0  — all checks pass
 *   1  — one or more mismatches found (CI should fail)
 */

import { readFileSync } from 'fs';
import { resolve } from 'path';

// ---------------------------------------------------------------------------
// CLI arg parsing
// ---------------------------------------------------------------------------

const args = process.argv.slice(2);
let dumpPath = '/tmp/contract-dump.json';

for (let i = 0; i < args.length; i++) {
  if (args[i] === '--dump' && args[i + 1]) {
    dumpPath = args[i + 1];
    i++;
  }
}

// ---------------------------------------------------------------------------
// Load the dump
// ---------------------------------------------------------------------------

interface GoField {
  name: string;
  type: string;
  json: string;
}

interface GoStruct {
  name: string;
  package: string;
  file: string;
  fields: GoField[];
}

interface ContractDump {
  structs: GoStruct[];
}

let dump: ContractDump;
try {
  const raw = readFileSync(dumpPath, 'utf-8');
  dump = JSON.parse(raw) as ContractDump;
} catch (e) {
  console.error(`check-contracts: failed to load dump from ${dumpPath}: ${e}`);
  console.error('Run: GOTOOLCHAIN=local CGO_ENABLED=0 go run ./tools/dump-contracts > /tmp/contract-dump.json');
  process.exit(1);
}

// ---------------------------------------------------------------------------
// Load types.ts and extract declared interface field names
// ---------------------------------------------------------------------------

const typesPath = resolve(process.cwd(), 'src/lib/types.ts');
let typesSource: string;
try {
  typesSource = readFileSync(typesPath, 'utf-8');
} catch (e) {
  console.error(`check-contracts: failed to read ${typesPath}: ${e}`);
  process.exit(1);
}

/**
 * Parse all interface declarations from the TypeScript source.
 * Returns a map of interface name → set of field names (as they appear in the source,
 * without type annotations).
 *
 * This is a simple regex-based parser — sufficient for the flat interface shapes
 * in types.ts. It does NOT handle nested interfaces or complex generics.
 */
function parseInterfaces(source: string): Map<string, Set<string>> {
  const result = new Map<string, Set<string>>();

  // Match: interface <Name> { ... }
  // Use a manual scan to correctly handle nested braces.
  const interfacePattern = /interface\s+(\w+)\s*\{/g;
  let match: RegExpExecArray | null;

  while ((match = interfacePattern.exec(source)) !== null) {
    const name = match[1];
    const bodyStart = match.index + match[0].length;

    // Find the matching closing brace
    let depth = 1;
    let pos = bodyStart;
    while (pos < source.length && depth > 0) {
      if (source[pos] === '{') depth++;
      else if (source[pos] === '}') depth--;
      pos++;
    }
    const body = source.slice(bodyStart, pos - 1);

    // Extract field names. Each field looks like:
    //   field_name: type;
    //   field_name?: type;
    //   /** comment */
    //   field_name: type;
    const fields = new Set<string>();
    const fieldPattern = /^\s*([\w_]+)\??\s*:/gm;
    let fm: RegExpExecArray | null;
    while ((fm = fieldPattern.exec(body)) !== null) {
      fields.add(fm[1]);
    }

    result.set(name, fields);
  }

  return result;
}

const tsInterfaces = parseInterfaces(typesSource);

// ---------------------------------------------------------------------------
// Mapping from Go struct name → TS interface name
// ---------------------------------------------------------------------------

/**
 * Most Go structs map to an identically-named TS interface.
 * Exceptions are listed here.
 *
 * Some Go structs (RawEvent, PricingTable, PerTokenRates) are informational
 * and included in types.ts for completeness — they are not part of any HTTP
 * response but are in the connector contract.
 */
const GO_TO_TS_NAME: Record<string, string> = {
  // connectors package
  Message: 'Message',
  Session: 'Session',
  ToolCall: 'ToolCall',
  ToolResult: 'ToolResult',
  PerTokenRates: 'PerTokenRates',
  PricingTable: 'PricingTable',
  RawEvent: 'RawEvent',
  // api package — contracts.go
  SessionListResponse: 'SessionListResponse',
  SessionResponse: 'SessionResponse',
  MessageListResponse: 'MessageListResponse',
  RestoreResponse: 'RestoreResponse',
  SummaryResponse: 'SummaryResponse',
  SearchHit: 'SearchHit',
  SearchResponse: 'SearchResponse',
  CostBucket: 'CostBucket',
  CostSummaryResponse: 'CostSummaryResponse',
  TaskModel: 'TaskModel',
  SettingsAI: 'SettingsAI',
  DetectedProviders: 'DetectedProviders',
  SettingsResponse: 'SettingsResponse',
  SettingsUpdateRequest: 'SettingsUpdateRequest',
  WizardConnectors: 'WizardConnectors',
  WizardRecommendation: 'WizardRecommendation',
  WizardDetectResponse: 'WizardDetectResponse',
  HealthzResponse: 'HealthzResponse',
  // api package — sse_events.go
  MsgNew: 'MsgNew',
  SummaryReady: 'SummaryReady',
  SessionUpdate: 'SessionUpdate',
  CostTick: 'CostTick',
  ThreadRebuild: 'ThreadRebuild',
  CompactDetected: 'CompactDetected'
};

// ---------------------------------------------------------------------------
// Run checks
// ---------------------------------------------------------------------------

let errors = 0;
let checked = 0;

for (const goStruct of dump.structs) {
  const tsName = GO_TO_TS_NAME[goStruct.name];
  if (!tsName) {
    // Unknown struct — not necessarily an error (could be internal-only).
    // Log as a warning but don't fail.
    console.warn(`  WARN  Go struct ${goStruct.package}.${goStruct.name} has no TS mapping — skipped`);
    continue;
  }

  const tsFields = tsInterfaces.get(tsName);
  if (!tsFields) {
    console.error(`  FAIL  TS interface '${tsName}' not found in types.ts (Go: ${goStruct.package}.${goStruct.name})`);
    errors++;
    continue;
  }

  for (const field of goStruct.fields) {
    if (!field.json || field.json === '-') {
      // No JSON tag or explicitly excluded — skip.
      continue;
    }
    const jsonName = field.json;
    if (!tsFields.has(jsonName)) {
      console.error(
        `  FAIL  ${goStruct.package}.${goStruct.name}.${field.name} (json:"${jsonName}") ` +
        `is missing from TS interface '${tsName}'`
      );
      errors++;
    } else {
      checked++;
    }
  }
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------

if (errors > 0) {
  console.error(`\ncheck-contracts: ${errors} field(s) missing from types.ts.`);
  console.error('Open a contract-change PR or add the missing fields to ui/src/lib/types.ts.\n');
  process.exit(1);
} else {
  console.log(`check-contracts: OK — ${checked} Go→TS field mappings verified.`);
}
