<script lang="ts">
  let { value, scroll = false, label }: { value: unknown; scroll?: boolean; label?: string } = $props();

  function esc(s: string): string {
    return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] ?? c);
  }

  /** Escaped, highlighted JSON. Tokens are matched on the raw text, then escaped, so no markup leaks in. */
  function highlight(v: unknown): string {
    if (v === undefined) return '<span class="j-lit">undefined</span>';
    const src = JSON.stringify(v, null, 2) ?? 'undefined';
    const re = /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?)/g;
    let out = '';
    let last = 0;
    for (const m of src.matchAll(re)) {
      out += esc(src.slice(last, m.index));
      if (m[1] !== undefined) out += m[2] ? `<span class="j-key">${esc(m[1])}</span>${esc(m[2])}` : `<span class="j-str">${esc(m[1])}</span>`;
      else if (m[3] !== undefined) out += `<span class="j-lit">${m[3]}</span>`;
      else out += `<span class="j-num">${esc(m[4])}</span>`;
      last = (m.index ?? 0) + m[0].length;
    }
    return out + esc(src.slice(last));
  }

  let html = $derived(highlight(value));
</script>

<pre class="code" class:scroll aria-label={label}>{@html html}</pre>
