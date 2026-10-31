// Tiny reactive data loader: re-runs when the reactive values read by `fn` change.
// Create it during component initialisation (it registers an $effect).

export class Query<T> {
  data: T | undefined = $state(undefined);
  error: unknown = $state(null);
  loading = $state(true);
  #seq = 0;
  #fn: () => Promise<T>;

  constructor(fn: () => Promise<T>, opts: { auto?: boolean } = {}) {
    this.#fn = fn;
    if (opts.auto !== false) {
      $effect(() => {
        void this.run();
      });
    }
  }

  run = async (): Promise<void> => {
    const my = ++this.#seq;
    this.loading = true;
    try {
      const d = await this.#fn();
      if (my === this.#seq) {
        this.data = d;
        this.error = null;
      }
    } catch (e) {
      if (my === this.#seq) this.error = e;
    } finally {
      if (my === this.#seq) this.loading = false;
    }
  };

  reload = (): Promise<void> => this.run();
}

export function query<T>(fn: () => Promise<T>, opts?: { auto?: boolean }): Query<T> {
  return new Query(fn, opts);
}

/** Cursor pagination helper ("Load more"). */
export class Paged<T> {
  items: T[] = $state([]);
  next: string | undefined = $state(undefined);
  loading = $state(false);
  error: unknown = $state(null);
  #seq = 0;
  #fn: (cursor?: string) => Promise<{ items: T[]; nextCursor?: string }>;

  constructor(fn: (cursor?: string) => Promise<{ items: T[]; nextCursor?: string }>, opts: { auto?: boolean } = {}) {
    this.#fn = fn;
    if (opts.auto !== false) {
      $effect(() => {
        void this.reset();
      });
    }
  }

  reset = async (): Promise<void> => {
    const my = ++this.#seq;
    this.loading = true;
    try {
      const r = await this.#fn(undefined);
      if (my !== this.#seq) return;
      this.items = r.items;
      this.next = r.nextCursor;
      this.error = null;
    } catch (e) {
      if (my === this.#seq) this.error = e;
    } finally {
      if (my === this.#seq) this.loading = false;
    }
  };

  more = async (): Promise<void> => {
    if (!this.next || this.loading) return;
    const my = this.#seq;
    this.loading = true;
    try {
      const r = await this.#fn(this.next);
      if (my !== this.#seq) return;
      this.items = [...this.items, ...r.items];
      this.next = r.nextCursor;
    } catch (e) {
      this.error = e;
    } finally {
      this.loading = false;
    }
  };
}
