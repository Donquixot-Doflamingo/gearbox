import type { Role } from '../api/types.gen';

const RANK: Record<Role, number> = { viewer: 0, author: 1, approver: 2, admin: 3 };

export type Action = 'read' | 'edit' | 'approve' | 'release' | 'admin';
const NEEDS: Record<Action, Role> = { read: 'viewer', edit: 'author', approve: 'approver', release: 'approver', admin: 'admin' };

export function can(role: Role | undefined, action: Action): boolean {
  if (!role) return false;
  return RANK[role] >= RANK[NEEDS[action]];
}

export function needsText(action: Action): string {
  return `Needs the ${NEEDS[action]} role.`;
}
