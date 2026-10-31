// One import for screens: `import { api, ApiError } from '$lib/api'`.
export * as api from './operations.gen';
export type * from './types.gen';
export { ApiError, errorText, isMock, apiBaseUrl, configureApi, onMockChange, setCsrfToken } from './client';
