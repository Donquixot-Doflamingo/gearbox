// RFC 4180-ish CSV parsing and writing.

export function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = '';
  let q = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (q) {
      if (c === '"' && text[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (c === '"') q = false;
      else cell += c;
      continue;
    }
    if (c === '"' && cell === '') q = true;
    else if (c === ',') {
      row.push(cell);
      cell = '';
    } else if (c === '\n' || c === '\r') {
      if (c === '\r' && text[i + 1] === '\n') i++;
      row.push(cell);
      rows.push(row);
      row = [];
      cell = '';
    } else cell += c;
  }
  if (cell !== '' || row.length) {
    row.push(cell);
    rows.push(row);
  }
  return rows.filter((r) => !(r.length === 1 && r[0].trim() === ''));
}

function esc(v: string): string {
  return /[",\n\r]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
}

export function toCsv(rows: (string | number | boolean | null | undefined)[][]): string {
  return rows.map((r) => r.map((v) => esc(v === null || v === undefined ? '' : String(v))).join(',')).join('\n') + '\n';
}

/** One member per line, optional header `key`. */
export function parseMemberList(text: string): string[] {
  const t = text.trim();
  if (t.startsWith('[') || t.startsWith('{')) {
    const v = JSON.parse(t);
    const arr = Array.isArray(v) ? v : Array.isArray(v?.members) ? v.members : null;
    if (!arr) throw new Error('JSON must be a list or {"members": [...]}');
    return arr.map(String);
  }
  const rows = parseCsv(t).map((r) => r[0].trim()).filter(Boolean);
  if (rows[0]?.toLowerCase() === 'key') rows.shift();
  return rows;
}
