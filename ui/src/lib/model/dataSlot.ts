// Data slots hold literal JSON; a string starting with "=" is an expression and "==" escapes a literal "=".

export function isExprSlot(v: unknown): v is string {
  return typeof v === 'string' && v.startsWith('=') && !v.startsWith('==');
}

/** The expression text of an `=expr` slot. */
export function slotExpr(v: string): string {
  return v.slice(1);
}

/** Store text typed by an author as a literal string (escaping a leading "="). */
export function literalString(text: string): string {
  return text.startsWith('=') ? '=' + text : text;
}

/** The literal text of a stored string (undoing the "==" escape). */
export function displayString(v: string): string {
  return v.startsWith('==') ? v.slice(1) : v;
}

/** Resolve every `=expr` in a data slot with `evalExpr`, recursively. */
export function resolveData(v: unknown, evalExpr: (expr: string) => unknown): unknown {
  if (isExprSlot(v)) return evalExpr(slotExpr(v));
  if (typeof v === 'string') return displayString(v);
  if (Array.isArray(v)) return v.map((x) => resolveData(x, evalExpr));
  if (v && typeof v === 'object') return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, resolveData(x, evalExpr)]));
  return v;
}

/** Partial match used by test `expect`: every listed path must equal; others are ignored. */
export function partialMismatches(expected: unknown, actual: unknown, path = ''): { path: string; expected: unknown; actual: unknown }[] {
  if (expected && typeof expected === 'object' && !Array.isArray(expected)) {
    if (!actual || typeof actual !== 'object' || Array.isArray(actual)) return [{ path: path || '/', expected, actual }];
    return Object.entries(expected).flatMap(([k, v]) => partialMismatches(v, (actual as Record<string, unknown>)[k], `${path}/${k}`));
  }
  return JSON.stringify(expected) === JSON.stringify(actual ?? null) ? [] : [{ path: path || '/', expected, actual: actual ?? null }];
}
