import { errorText } from '../api/client';

export interface Toast {
  id: number;
  message: string;
  kind: 'info' | 'crit';
  action?: { label: string; run: () => void };
}

export const toasts: Toast[] = $state([]);
let seq = 0;

export function dismiss(id: number): void {
  const i = toasts.findIndex((t) => t.id === id);
  if (i >= 0) toasts.splice(i, 1);
}

export function toast(message: string, opts: { kind?: 'info' | 'crit'; action?: Toast['action']; ms?: number } = {}): number {
  const id = ++seq;
  toasts.push({ id, message, kind: opts.kind ?? 'info', action: opts.action });
  if (toasts.length > 4) toasts.splice(0, toasts.length - 4);
  setTimeout(() => dismiss(id), opts.ms ?? (opts.action ? 7000 : 4200));
  return id;
}

export function toastError(e: unknown, prefix = ''): void {
  toast(prefix ? `${prefix}: ${errorText(e)}` : errorText(e), { kind: 'crit', ms: 6500 });
}
