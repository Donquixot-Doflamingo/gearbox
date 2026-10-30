import type { Condition, ValidationProblem } from '$lib/api/types.gen';
import type { CondPath, GroupKind, LeafKind } from '$lib/model/condition';
import type { FieldInfo } from '$lib/model/schemaTree';

/** What every node of the condition builder can do; provided by ConditionBuilder. */
export interface CondCtx {
  app: string;
  fields: FieldInfo[] | null;
  disabled: boolean;
  /** Label for the thing conditions read (e.g. "eligibility_input@1"). */
  subject: string;
  replace(path: CondPath, next: Condition): void;
  remove(path: CondPath): void;
  append(path: CondPath, child: Condition): void;
  wrap(path: CondPath, kind: GroupKind): void;
  unwrap(path: CondPath): void;
  move(groupPath: CondPath, from: number, to: number): void;
  setGroupKind(path: CondPath, kind: 'all' | 'any'): void;
  setLeafKind(path: CondPath, kind: LeafKind): void;
  /** Problems (local and from the server) for exactly this node. */
  problemsAt(path: CondPath): (ValidationProblem | { message: string; severity: 'error' | 'warning' })[];
}
