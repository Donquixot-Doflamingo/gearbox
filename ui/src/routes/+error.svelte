<script lang="ts">
  import { page } from '$app/state';
  import { href } from '$lib/nav.svelte';
</script>

<main class="layout" style="grid-template-columns:minmax(0,1fr)">
  <div class="empty">
    <h2>{page.status === 404 ? 'Page not found' : 'Something went wrong'}</h2>
    <p>{page.error?.message ?? 'Unknown error'}</p>
    <div class="actions"><a class="btn btn-primary" href={href('/')}>Go to Gearbox</a></div>
  </div>
</main>
