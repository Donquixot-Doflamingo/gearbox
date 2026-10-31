<script lang="ts">
  // Sign-in hand-off: the API runs the OIDC flow (GET /auth/login) and sets the session cookie.
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { apiBaseUrl } from '$lib/api/client';
  import { mode } from '$lib/stores/mode.svelte';
  import { href } from '$lib/nav.svelte';

  let returnTo = $derived(page.url.searchParams.get('returnTo') || href('/'));
  let signedOut = $derived(page.url.searchParams.get('signedOut') === '1');
  let loginUrl = $derived(`${apiBaseUrl().replace(/\/$/, '')}/auth/login?returnTo=${encodeURIComponent(new URL(returnTo, location.origin).toString())}`);

  $effect(() => {
    if (!signedOut && !mode.mock) location.assign(loginUrl);
  });
</script>

<main class="layout" style="grid-template-columns:minmax(0,1fr);max-width:560px">
  <div class="panel">
    <a class="brand" href={href('/')}>
      <svg width="22" height="22" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="4" stroke-dasharray="3.1 3.18" /><circle cx="12" cy="12" r="6.2" fill="none" stroke="currentColor" stroke-width="2.2" /><circle cx="12" cy="12" r="2" fill="currentColor" /></svg>
      <span>Gearbox</span>
    </a>
    {#if signedOut}
      <h1>You are signed out</h1>
      <p class="sub">Sign in again with your organisation's identity provider.</p>
    {:else}
      <h1>Sign in</h1>
      <p class="sub">Redirecting to your identity provider…</p>
    {/if}
    {#if mode.mock}
      <p class="hint">Sample data mode: there is no identity provider; continue as the sample user.</p>
      <div class="actions"><button type="button" class="btn btn-primary" onclick={() => goto(returnTo)}>Continue</button></div>
    {:else}
      <div class="actions"><a class="btn btn-primary" href={loginUrl}>Sign in</a></div>
    {/if}
  </div>
</main>
