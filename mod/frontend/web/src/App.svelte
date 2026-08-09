<script>
  const sections = [
    'Repositories',
    'Plugins',
    'Versions',
    'Marketplaces',
    'Teams',
    'Audit',
  ];
</script>

<svelte:head>
  <title>MarketplaceServer</title>
</svelte:head>

<main class="min-h-screen bg-slate-950 text-slate-100">
  <section class="mx-auto flex min-h-screen max-w-5xl flex-col justify-center px-6 py-16">
    <p class="text-sm font-semibold uppercase tracking-[0.35em] text-sky-300">Claude Code Marketplace</p>
    <h1 class="mt-5 text-4xl font-bold tracking-tight sm:text-6xl">MarketplaceServer</h1>
    <p class="mt-6 max-w-2xl text-lg leading-8 text-slate-300">
      Manage plugin repositories, immutable versions, teams, and published marketplace indexes from a single static console.
    </p>

    <div class="mt-10 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      {#each sections as section}
        <div class="rounded-xl border border-slate-800 bg-slate-900/70 px-4 py-3 text-sm text-slate-200 shadow-sm">
          {section}
        </div>
      {/each}
    </div>
  </section>
</main>
