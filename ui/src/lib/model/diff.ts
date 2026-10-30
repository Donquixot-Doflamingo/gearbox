// Line diff (LCS) and unified-diff rendering helpers.

export type DiffOp = ' ' | '+' | '-';
export interface DiffLine {
  op: DiffOp;
  text: string;
}

export function diffLines(a: string, b: string): DiffLine[] {
  const x = a.split('\n'), y = b.split('\n');
  if (x.at(-1) === '') x.pop();
  if (y.at(-1) === '') y.pop();
  const n = x.length, m = y.length;
  // LCS table (fine for specs of a few thousand lines)
  const dp: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) dp[i][j] = x[i] === y[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const out: DiffLine[] = [];
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (x[i] === y[j]) {
      out.push({ op: ' ', text: x[i] });
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) out.push({ op: '-', text: x[i++] });
    else out.push({ op: '+', text: y[j++] });
  }
  while (i < n) out.push({ op: '-', text: x[i++] });
  while (j < m) out.push({ op: '+', text: y[j++] });
  return out;
}

/** Unified diff text with @@ hunks and `context` lines around changes. */
export function unifiedDiff(a: string, b: string, fromLabel = 'a', toLabel = 'b', context = 3): string {
  const lines = diffLines(a, b);
  if (!lines.some((l) => l.op !== ' ')) return '';
  const keep = new Array(lines.length).fill(false);
  lines.forEach((l, i) => {
    if (l.op !== ' ') for (let k = Math.max(0, i - context); k <= Math.min(lines.length - 1, i + context); k++) keep[k] = true;
  });
  let out = `--- ${fromLabel}\n+++ ${toLabel}\n`;
  let ai = 1, bi = 1;
  let i = 0;
  while (i < lines.length) {
    if (!keep[i]) {
      if (lines[i].op !== '+') ai++;
      if (lines[i].op !== '-') bi++;
      i++;
      continue;
    }
    const start = i;
    const as = ai, bs = bi;
    let body = '';
    while (i < lines.length && keep[i]) {
      body += `${lines[i].op}${lines[i].text}\n`;
      if (lines[i].op !== '+') ai++;
      if (lines[i].op !== '-') bi++;
      i++;
    }
    const seg = lines.slice(start, i);
    out += `@@ -${as},${seg.filter((l) => l.op !== '+').length} +${bs},${seg.filter((l) => l.op !== '-').length} @@\n${body}`;
  }
  return out;
}

export type UnifiedKind = 'add' | 'del' | 'ctx' | 'hunk' | 'file';
/** Classify lines of a unified diff for rendering. */
export function parseUnified(text: string): { kind: UnifiedKind; text: string }[] {
  return text
    .split('\n')
    .filter((l, i, all) => !(l === '' && i === all.length - 1))
    .map((l) => {
      if (l.startsWith('+++') || l.startsWith('---')) return { kind: 'file' as const, text: l };
      if (l.startsWith('@@')) return { kind: 'hunk' as const, text: l };
      if (l.startsWith('+')) return { kind: 'add' as const, text: l };
      if (l.startsWith('-')) return { kind: 'del' as const, text: l };
      return { kind: 'ctx' as const, text: l };
    });
}
