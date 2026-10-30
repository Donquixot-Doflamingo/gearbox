import { isMock, onMockChange } from '../api/client';

export type Theme = 'system' | 'light' | 'dark';

function readTheme(): Theme {
  try {
    const t = globalThis.localStorage?.getItem('gearbox-theme');
    return t === 'light' || t === 'dark' ? t : 'system';
  } catch {
    return 'system';
  }
}

class ModeStore {
  mock = $state(isMock());
  theme: Theme = $state(readTheme());

  setTheme(t: Theme): void {
    this.theme = t;
    try {
      if (t === 'system') globalThis.localStorage?.removeItem('gearbox-theme');
      else globalThis.localStorage?.setItem('gearbox-theme', t);
    } catch {
      /* private mode */
    }
    const root = globalThis.document?.documentElement;
    if (!root) return;
    if (t === 'system') delete root.dataset.theme;
    else root.dataset.theme = t;
  }

  cycleTheme(): void {
    this.setTheme(this.theme === 'system' ? 'light' : this.theme === 'light' ? 'dark' : 'system');
  }
}

export const mode = new ModeStore();
onMockChange((on) => (mode.mock = on));
