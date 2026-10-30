// /apps/<app>/decisions/<name>/nodes/<node> opens the decision editor with that node selected.
import { redirect } from '@sveltejs/kit';

export function load({ params, url }: { params: Record<string, string>; url: URL }): never {
  const q = new URLSearchParams(url.search);
  q.set('node', params.node);
  redirect(307, `${url.pathname.replace(/\/nodes\/[^/]+\/?$/, '')}?${q.toString()}`);
}
