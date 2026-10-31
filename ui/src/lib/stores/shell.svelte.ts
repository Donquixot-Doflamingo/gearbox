// Values the app shell loads once and screens read (environment names of the current app).
export const shell: { envs: string[] } = $state({ envs: ['dev', 'stage', 'prod'] });
