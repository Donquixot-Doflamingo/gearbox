import type { Transport } from '../client';
export const mockTransport: Transport = async () => ({ status: 501, body: { title: 'todo', status: 501, code: 'TODO' } });
