<script lang="ts">
  // Brand logo for an app shortcut, matched by app name. Unknown names fall back
  // to a short text label so a new backend app still renders something sensible.
  let { name, class: cls = 'h-6 w-6' }: { name: string; class?: string } = $props()
</script>

{#if name === 'YouTube'}
  <svg class={cls} viewBox="0 0 24 24" aria-hidden="true">
    <rect x="2" y="5" width="20" height="14" rx="4.2" fill="#FF0000" />
    <path d="M10 8.5 16 12l-6 3.5z" fill="#fff" />
  </svg>
{:else if name === 'Netflix'}
  <svg class={cls} viewBox="0 0 24 24" aria-hidden="true">
    <rect x="6" y="3" width="2.7" height="18" fill="#B1060F" />
    <rect x="15.3" y="3" width="2.7" height="18" fill="#B1060F" />
    <polygon points="6,3 8.7,3 18,21 15.3,21" fill="#E50914" />
  </svg>
{:else if name === 'Prime Video'}
  <svg class={cls} viewBox="0 0 24 24" aria-hidden="true">
    <rect x="2" y="4" width="20" height="16" rx="4.4" fill="#1399FF" />
    <path d="M9.8 8 15.4 11.2 9.8 14.4z" fill="#fff" />
    <path d="M5.4 15.6c2.9 2.4 8 2.6 11.4 0 .6-.45 1.1-1 1.5-1.7" fill="none" stroke="#fff" stroke-width="1.5" stroke-linecap="round" />
  </svg>
{:else if name === 'Spotify'}
  <svg class={cls} viewBox="0 0 24 24" aria-hidden="true">
    <circle cx="12" cy="12" r="10" fill="#1DB954" />
    <path d="M6.8 10c3.4-1 7.6-.6 10.6 1.2" stroke="#fff" stroke-width="1.5" fill="none" stroke-linecap="round" />
    <path d="M7.4 13c2.8-.8 6.2-.4 8.6 1" stroke="#fff" stroke-width="1.4" fill="none" stroke-linecap="round" />
    <path d="M8 15.6c2.2-.6 4.8-.3 6.6.8" stroke="#fff" stroke-width="1.2" fill="none" stroke-linecap="round" />
  </svg>
{:else if name === 'Apple TV'}
  <svg class={cls} viewBox="0 0 24 24" aria-hidden="true">
    <rect x="2" y="4" width="20" height="16" rx="4.5" fill="#111" />
    <text x="12" y="15.6" text-anchor="middle" font-family="-apple-system, Helvetica, Arial, sans-serif" font-size="9.5" font-weight="700" fill="#fff">tv</text>
  </svg>
{:else if name === 'Apple Music'}
  <svg class={cls} viewBox="0 0 24 24" aria-hidden="true">
    <defs>
      <linearGradient id="am-grad" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stop-color="#FB5C74" />
        <stop offset="1" stop-color="#FA233B" />
      </linearGradient>
    </defs>
    <rect x="2" y="3" width="20" height="18" rx="5" fill="url(#am-grad)" />
    <path d="M9.3 14.6V8L15.4 6.6V13.2" fill="none" stroke="#fff" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round" />
    <ellipse cx="7.7" cy="14.9" rx="2.1" ry="1.8" fill="#fff" />
    <ellipse cx="13.8" cy="13.5" rx="2.1" ry="1.8" fill="#fff" />
  </svg>
{:else if name === 'Skypro'}
  <!-- Retro TV with a rainbow screen + antennas (Skypro logo). The frame uses
       currentColor — navy in light mode, white in dark — so the body gets a
       visible outline on dark backgrounds where the navy would otherwise vanish. -->
  <svg class="{cls} text-[#16243F] dark:text-white" viewBox="0 0 64 64" aria-hidden="true">
    <defs>
      <clipPath id="sp-screen"><rect x="15" y="30" width="34" height="22" rx="5" /></clipPath>
    </defs>
    <g stroke="currentColor" stroke-width="3" stroke-linecap="round">
      <line x1="29" y1="27" x2="18" y2="9" />
      <line x1="34" y1="27" x2="46" y2="6" />
    </g>
    <circle cx="18" cy="8.5" r="3.4" fill="currentColor" />
    <circle cx="46" cy="5.5" r="3.4" fill="currentColor" />
    <rect x="9" y="24" width="46" height="33" rx="8" fill="#16243F" stroke="currentColor" stroke-width="2.6" />
    <rect x="15" y="30" width="34" height="22" rx="5" fill="#fff" />
    <g clip-path="url(#sp-screen)" fill="none" stroke-width="6.3">
      <circle cx="14" cy="53" r="40" stroke="#E5332A" />
      <circle cx="14" cy="53" r="33.6" stroke="#F47C20" />
      <circle cx="14" cy="53" r="27.2" stroke="#FBC50B" />
      <circle cx="14" cy="53" r="20.8" stroke="#54B948" />
      <circle cx="14" cy="53" r="14.4" stroke="#1C9AD6" />
    </g>
    <circle cx="51.3" cy="44.5" r="1.5" fill="#fff" />
    <circle cx="51.3" cy="49.5" r="1.5" fill="#fff" />
  </svg>
{:else if name === 'HDMI'}
  <svg class={cls} viewBox="0 0 24 24" aria-hidden="true">
    <rect x="1.5" y="6.5" width="21" height="11" rx="2.5" fill="#475569" />
    <text x="12" y="14.4" text-anchor="middle" font-family="-apple-system, Helvetica, Arial, sans-serif" font-size="6.4" font-weight="700" letter-spacing="0.3" fill="#fff">HDMI</text>
  </svg>
{:else}
  <span class="text-xs font-medium">{name}</span>
{/if}
