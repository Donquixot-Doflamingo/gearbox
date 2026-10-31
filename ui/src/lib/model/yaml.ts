// Minimal YAML emitter (block style) for readable diffs and exports. Not a parser.

const PLAIN = /^[A-Za-z_][A-Za-z0-9_ .\/-]*$/;
const RESERVED = /^(true|false|null|yes|no|on|off|~|-?\d+(\.\d+)?([eE][-+]?\d+)?)$/i;

function scalar(v: unknown): string {
  if (v === null || v === undefined) return 'null';
  if (typeof v === 'string') {
    if (v === '' || !PLAIN.test(v) || RESERVED.test(v) || v.endsWith(' ')) return JSON.stringify(v);
    return v;
  }
  return String(v);
}

function key(k: string): string {
  return /^[A-Za-z_][A-Za-z0-9_-]*$/.test(k) ? k : JSON.stringify(k);
}

function isFlatScalarList(v: unknown[]): boolean {
  return v.length <= 8 && v.every((x) => x === null || typeof x !== 'object') && JSON.stringify(v).length < 70;
}

export function toYaml(v: unknown, indent = 0): string {
  const pad = ' '.repeat(indent);
  if (Array.isArray(v)) {
    if (v.length === 0) return `${pad}[]\n`;
    return v
      .map((item) => {
        if (item && typeof item === 'object' && !(Array.isArray(item) && isFlatScalarList(item))) {
          const inner = toYaml(item, indent + 2).replace(/^\s+/, '');
          return `${pad}- ${inner}`;
        }
        return `${pad}- ${Array.isArray(item) ? '[' + item.map(scalar).join(', ') + ']' : scalar(item)}\n`;
      })
      .join('');
  }
  if (v && typeof v === 'object') {
    const entries = Object.entries(v).filter(([, x]) => x !== undefined);
    if (entries.length === 0) return `${pad}{}\n`;
    return entries
      .map(([k, x]) => {
        if (Array.isArray(x)) {
          if (x.length === 0) return `${pad}${key(k)}: []\n`;
          if (isFlatScalarList(x)) return `${pad}${key(k)}: [${x.map(scalar).join(', ')}]\n`;
          return `${pad}${key(k)}:\n${toYaml(x, indent + 2)}`;
        }
        if (x && typeof x === 'object') {
          if (Object.keys(x).length === 0) return `${pad}${key(k)}: {}\n`;
          return `${pad}${key(k)}:\n${toYaml(x, indent + 2)}`;
        }
        if (typeof x === 'string' && x.includes('\n')) {
          return `${pad}${key(k)}: |\n${x.split('\n').map((l) => `${pad}  ${l}`).join('\n')}\n`;
        }
        return `${pad}${key(k)}: ${scalar(x)}\n`;
      })
      .join('');
  }
  return `${pad}${scalar(v)}\n`;
}
