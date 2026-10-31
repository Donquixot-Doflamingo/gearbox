// Editing state for one versioned resource: load, track changes, validate as you type, save with
// optimistic concurrency (409 REVISION_CONFLICT), and cut versions.

import { api } from './api';
import { ApiError } from './api/client';
import type { Resource, ResourceKind, ValidationProblem, ValidationResult } from './api/types.gen';
import { toast, toastError } from './stores/toast.svelte';

function clone<T>(v: T): T {
  return v === undefined ? v : (JSON.parse(JSON.stringify(v)) as T);
}

export class DraftEditor<S = unknown> {
  resource: Resource | null = $state(null);
  spec: S = $state(undefined as S);
  description = $state('');
  revision = $state(0);
  /** JSON of the last loaded/saved spec + description. */
  saved = $state('');
  loading = $state(true);
  loadError: unknown = $state(null);
  saving = $state(false);
  saveError: ApiError | null = $state(null);
  conflict = $state(false);
  validation: ValidationResult | null = $state(null);
  validating = $state(false);
  /** Bumps whenever the server copy changes (save, reload) so children can refetch derived data. */
  version = $state(0);

  dirty = $derived(this.snapshot() !== this.saved);
  problems: ValidationProblem[] = $derived(this.validation?.problems ?? []);
  errorCount = $derived(this.problems.filter((p) => p.severity === 'error').length);

  #timer: ReturnType<typeof setTimeout> | null = null;
  #vseq = 0;

  constructor(
    readonly app: string,
    readonly kind: ResourceKind,
    readonly name: string,
  ) {}

  snapshot(): string {
    return JSON.stringify(this.spec ?? null) + '\u0000' + this.description;
  }

  #apply(r: Resource): void {
    this.resource = r;
    this.spec = clone(r.draft.spec) as S;
    this.description = r.description ?? '';
    this.revision = r.draft.revision;
    this.saved = this.snapshot();
    if (r.draft.validation) this.validation = r.draft.validation;
    this.version++;
  }

  async load(): Promise<void> {
    this.loading = true;
    try {
      this.#apply(await api.getResource({ app: this.app, kind: this.kind, name: this.name }));
      this.loadError = null;
      this.conflict = false;
    } catch (e) {
      this.loadError = e;
    } finally {
      this.loading = false;
    }
  }

  /** Reload the server copy, dropping local edits. */
  async discard(): Promise<void> {
    this.saveError = null;
    await this.load();
  }

  async save(): Promise<boolean> {
    if (this.saving) return false;
    this.saving = true;
    this.saveError = null;
    try {
      const r = await api.saveDraft({
        app: this.app,
        kind: this.kind,
        name: this.name,
        body: { revision: this.revision, spec: $state.snapshot(this.spec) as Resource['draft']['spec'], description: this.description || undefined },
      });
      this.#apply(r);
      this.conflict = false;
      toast(`Saved draft of ${this.name} (revision ${r.draft.revision})`);
      return true;
    } catch (e) {
      if (e instanceof ApiError) {
        this.saveError = e;
        if (e.code === 'REVISION_CONFLICT') this.conflict = true;
        else toastError(e, 'Not saved');
      } else toastError(e, 'Not saved');
      return false;
    } finally {
      this.saving = false;
    }
  }

  /** After a conflict: take the server revision but keep these edits, then save again. */
  async overwrite(): Promise<boolean> {
    try {
      const r = await api.getResource({ app: this.app, kind: this.kind, name: this.name });
      this.revision = r.draft.revision;
      this.resource = r;
    } catch (e) {
      toastError(e);
      return false;
    }
    return this.save();
  }

  /** Debounced POST …/validate with the current (unsaved) spec. */
  scheduleValidate(delay = 450): void {
    if (this.#timer) clearTimeout(this.#timer);
    this.#timer = setTimeout(() => void this.validateNow(), delay);
  }

  async validateNow(): Promise<ValidationResult | null> {
    const my = ++this.#vseq;
    this.validating = true;
    try {
      const r = await api.validateResource({ app: this.app, kind: this.kind, name: this.name, body: { spec: $state.snapshot(this.spec) as Resource['draft']['spec'] } });
      if (my === this.#vseq) this.validation = r;
      return r;
    } catch (e) {
      if (my === this.#vseq && !(e instanceof ApiError && e.status === 0)) this.validation = { valid: false, problems: [{ code: 'VALIDATE_FAILED', severity: 'warning', path: '', message: `Could not validate: ${(e as Error).message}` }] };
      return null;
    } finally {
      if (my === this.#vseq) this.validating = false;
    }
  }

  /** Field error from the last failed save (JSON pointer into the spec, without the /spec prefix). */
  fieldError(ptr: string): string | undefined {
    const e = this.saveError;
    if (!e) return undefined;
    return e.fieldError('/spec' + ptr) ?? e.fieldError(ptr);
  }

  problemsUnder(ptr: string): ValidationProblem[] {
    const fromSave: ValidationProblem[] = (this.saveError?.errors ?? [])
      .map((x) => ({ code: x.code ?? this.saveError!.code, severity: 'error' as const, path: x.path.replace(/^\/spec/, ''), message: x.message }))
      .filter((p) => p.path === ptr || p.path.startsWith(ptr + '/'));
    return [...fromSave, ...this.problems.filter((p) => p.path === ptr || p.path.startsWith(ptr + '/'))];
  }
}
