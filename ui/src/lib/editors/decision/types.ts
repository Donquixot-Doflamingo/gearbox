// Editable shapes of nodes: required schema refs may be missing while the author is still choosing.
import type { RulesNode, SchemaRef, TableNode } from '$lib/api/types.gen';

export type RulesDraft = Omit<RulesNode, 'inputSchema' | 'outputSchema'> & { inputSchema?: SchemaRef; outputSchema?: SchemaRef };
export type TableDraft = Omit<TableNode, 'inputSchema' | 'outputSchema'> & { inputSchema?: SchemaRef; outputSchema?: SchemaRef };

/** Problems for one node, keyed by JSON pointer prefix. */
export interface ProblemSource {
  problemsUnder(ptr: string): { code: string; severity: 'error' | 'warning'; path: string; message: string }[];
}
