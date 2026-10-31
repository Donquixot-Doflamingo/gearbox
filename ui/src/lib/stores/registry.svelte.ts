// Shared caches the editors read: resource lists per kind (for pickers), schema versions (for field
// pickers and data editors) and resource drafts (segment types, experiment variants).

import { api } from '../api';
import type { Resource, ResourceKind, ResourceSummary } from '../api/types.gen';
import { isObj, type JsonSchema } from '../model/schemaTree';

class RegistryStore {
  lists: Record<string, ResourceSummary[]> = $state({});
  schemas: Record<string, JsonSchema | null> = $state({});
  resources: Record<string, Resource | null> = $state({});
  #inflight = new Set<string>();

  #fetch(key: string, run: () => Promise<void>): void {
    if (this.#inflight.has(key)) return;
    this.#inflight.add(key);
    // defer so reads inside templates never mutate state while rendering
    queueMicrotask(() => {
      run().finally(() => this.#inflight.delete(key));
    });
  }

  /** Resource summaries of a kind (loads on first read). */
  list(app: string, kind: ResourceKind): ResourceSummary[] | undefined {
    const key = `${app}/${kind}`;
    const v = this.lists[key];
    if (v === undefined && app)
      this.#fetch(key, () =>
        this.loadList(app, kind).then(
          () => undefined,
          () => {
            this.lists[key] = [];
          },
        ),
      );
    return v;
  }

  async loadList(app: string, kind: ResourceKind): Promise<ResourceSummary[]> {
    const all: ResourceSummary[] = [];
    let cursor: string | undefined;
    for (let i = 0; i < 20; i++) {
      const r = await api.listResources({ app, kind, query: { cursor, limit: 200 } });
      all.push(...r.items);
      cursor = r.nextCursor;
      if (!cursor) break;
    }
    this.lists[`${app}/${kind}`] = all;
    return all;
  }

  invalidate(app: string, kind: ResourceKind, name?: string): void {
    delete this.lists[`${app}/${kind}`];
    if (name) delete this.resources[`${app}/${kind}/${name}`];
  }

  /** A registered schema version's JSON Schema: undefined while loading, null when missing. */
  schema(app: string, name: string | undefined, version: number | undefined): JsonSchema | null | undefined {
    if (!app || !name || !version) return null;
    const key = `${app}/${name}@${version}`;
    if (!(key in this.schemas)) {
      this.#fetch(key, async () => {
        try {
          const v = await api.getVersion({ app, kind: 'schemas', name, version });
          const js = isObj(v.spec) && isObj((v.spec as { jsonSchema?: unknown }).jsonSchema) ? ((v.spec as { jsonSchema: JsonSchema }).jsonSchema) : null;
          this.schemas[key] = js;
        } catch {
          this.schemas[key] = null;
        }
      });
      return undefined;
    }
    return this.schemas[key];
  }

  /** A resource with its draft (cached). */
  resource(app: string, kind: ResourceKind, name: string | undefined): Resource | null | undefined {
    if (!app || !name) return null;
    const key = `${app}/${kind}/${name}`;
    if (!(key in this.resources)) {
      this.#fetch(key, async () => {
        try {
          this.resources[key] = await api.getResource({ app, kind, name });
        } catch {
          this.resources[key] = null;
        }
      });
      return undefined;
    }
    return this.resources[key];
  }
}

export const registry = new RegistryStore();
