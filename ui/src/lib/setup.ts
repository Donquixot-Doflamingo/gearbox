// Configure the API client once, from build-time settings (vite.config.ts `define`).
import { goto } from '$app/navigation';
import { base } from '$app/paths';
import { configureApi } from './api/client';

let done = false;

export function setupApi(): void {
  if (done) return;
  done = true;
  configureApi({
    baseUrl: __GEARBOX_API__ || '/api/v1',
    mock: __GEARBOX_MOCK__ === '1' || __GEARBOX_MOCK__ === 'true',
    // in dev, fall back to sample data when no API answers
    mockAuto: import.meta.env.DEV,
    onUnauthorized: (returnTo) => {
      if (location.pathname.startsWith(`${base}/auth/`)) return;
      void goto(`${base}/auth/login?returnTo=${encodeURIComponent(returnTo)}`);
    },
  });
}
