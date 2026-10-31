import { api } from '../api';
import type { Member } from '../api/types.gen';
import { can, needsText, type Action } from '../model/roles';

class SessionStore {
  member: Member | null = $state(null);
  loading = $state(true);
  error: unknown = $state(null);

  async load(): Promise<void> {
    this.loading = true;
    try {
      const s = await api.getSession({});
      this.member = s.member;
      this.error = null;
    } catch (e) {
      this.error = e;
      this.member = null;
    } finally {
      this.loading = false;
    }
  }

  can(action: Action): boolean {
    return can(this.member?.role, action);
  }

  /** Tooltip for a disabled control, or undefined when allowed. */
  why(action: Action): string | undefined {
    return this.can(action) ? undefined : needsText(action);
  }
}

export const session = new SessionStore();
