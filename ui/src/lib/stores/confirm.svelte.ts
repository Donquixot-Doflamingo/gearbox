// In-page confirmation dialog. Production-affecting actions pass `env` so the dialog repeats it.

export interface ConfirmOptions {
  title: string;
  /** Paragraphs of plain text. */
  body?: string | string[];
  /** Target environment, repeated prominently in the dialog. */
  env?: string;
  /** Extra line next to the environment (e.g. "generation 43"). */
  envDetail?: string;
  ok?: string;
  danger?: boolean;
  /** Ask for a reason (rollback, kill switch). */
  reason?: { label: string; required?: boolean; placeholder?: string };
  /** Require typing this text to enable the button (very destructive actions). */
  typeToConfirm?: string;
}

export interface ConfirmRequest extends ConfirmOptions {
  resolve: (r: { reason: string } | null) => void;
}

export const confirmState: { current: ConfirmRequest | null } = $state({ current: null });

/** Resolves to `{ reason }` when confirmed, `null` when cancelled. */
export function confirmAction(opts: ConfirmOptions): Promise<{ reason: string } | null> {
  confirmState.current?.resolve(null);
  return new Promise((resolve) => {
    confirmState.current = { ...opts, resolve };
  });
}

export function settleConfirm(r: { reason: string } | null): void {
  const cur = confirmState.current;
  confirmState.current = null;
  cur?.resolve(r);
}
