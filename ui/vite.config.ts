import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig, loadEnv } from 'vite';

// PUBLIC_GEARBOX_API  base URL of the control-plane API (default /api/v1)
// PUBLIC_GEARBOX_MOCK 1 = always use the in-browser mock API with sample data
// GEARBOX_PROXY       dev only: proxy /api to this origin (e.g. http://localhost:8080) so cookies stay same-origin
export default defineConfig(({ mode }: { mode: string }) => {
  const env = { ...loadEnv(mode, process.cwd(), ['PUBLIC_', 'GEARBOX_']), ...process.env };
  const proxy = env.GEARBOX_PROXY;
  return {
    plugins: [sveltekit()],
    define: {
      __GEARBOX_API__: JSON.stringify(env.PUBLIC_GEARBOX_API || '/api/v1'),
      __GEARBOX_MOCK__: JSON.stringify(env.PUBLIC_GEARBOX_MOCK || ''),
    },
    server: proxy ? { proxy: { '/api': { target: proxy, changeOrigin: true } } } : undefined,
    test: { include: ['src/**/*.test.ts'], environment: 'node' },
  };
});
