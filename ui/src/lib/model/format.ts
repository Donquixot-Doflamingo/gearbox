// Display formatting.

export function fmtNum(n: number | undefined | null, digits = 0): string {
  if (n === undefined || n === null || !Number.isFinite(n)) return '—';
  return n.toLocaleString('en-US', { maximumFractionDigits: digits, minimumFractionDigits: 0 });
}

/** 23 µs, 0.41 ms, 812 ms, 1.5 s */
export function fmtMicros(us: number | undefined | null): string {
  if (us === undefined || us === null || !Number.isFinite(us)) return '—';
  if (us < 1000) return `${fmtNum(us, us < 10 ? 1 : 0)} µs`;
  if (us < 1_000_000) return `${fmtNum(us / 1000, us < 10_000 ? 2 : 0)} ms`;
  return `${fmtNum(us / 1_000_000, 1)} s`;
}

export function fmtPct(rate: number | undefined | null, digits = 2): string {
  if (rate === undefined || rate === null || !Number.isFinite(rate)) return '—';
  return `${(rate * 100).toFixed(digits)}%`;
}

export function fmtBytes(b: number | undefined | null): string {
  if (b === undefined || b === null) return '—';
  if (b < 1024) return `${b} B`;
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
  return `${(b / 1024 / 1024).toFixed(1)} MB`;
}

const MIN = 60_000, HOUR = 60 * MIN, DAY = 24 * HOUR;

/** "just now", "5 min ago", "3 h ago", "2 d ago", else a date. */
export function relTime(iso: string | undefined | null, now = Date.now()): string {
  if (!iso) return '—';
  const t = Date.parse(iso);
  if (isNaN(t)) return iso;
  const d = now - t;
  if (d < 0) return fmtDateTime(iso);
  if (d < MIN) return 'just now';
  if (d < HOUR) return `${Math.floor(d / MIN)} min ago`;
  if (d < DAY) return `${Math.floor(d / HOUR)} h ago`;
  if (d < 14 * DAY) return `${Math.floor(d / DAY)} d ago`;
  return fmtDate(iso);
}

export function fmtDate(iso: string): string {
  const d = new Date(iso);
  return isNaN(d.getTime()) ? iso : d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: d.getFullYear() === new Date().getFullYear() ? undefined : 'numeric' });
}

export function fmtDateTime(iso: string | undefined | null): string {
  if (!iso) return '—';
  const d = new Date(iso);
  return isNaN(d.getTime()) ? iso : d.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false });
}

export function fmtTime(iso: string): string {
  const d = new Date(iso);
  return isNaN(d.getTime()) ? iso : d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' }) + '.' + String(d.getMilliseconds()).padStart(3, '0');
}

export function shortHash(h: string | undefined | null): string {
  if (!h) return '—';
  const m = /^(sha256:)?([0-9a-f…]+)/i.exec(h);
  return m ? `${m[1] ?? ''}${m[2].slice(0, 10)}` : h.slice(0, 16);
}

export function plural(n: number, one: string, many = one + 's'): string {
  return `${fmtNum(n)} ${n === 1 ? one : many}`;
}

export function initials(name: string | undefined, email = ''): string {
  const src = (name || email.split('@')[0] || '?').trim();
  const parts = src.split(/[\s._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? '?') + (parts[1]?.[0] ?? '')).toUpperCase();
}

export function prettyJson(v: unknown): string {
  return v === undefined ? '' : JSON.stringify(v, null, 2);
}
