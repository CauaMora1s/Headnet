<script lang="ts">
  import StatusBadge from '../lib/StatusBadge.svelte';
  import { ApiError, type HealthResponse, type VersionResponse } from '../lib/api';
  import { client } from '../lib/session.svelte';
  import { PLANNED_AREAS } from '../lib/roadmap';

  let health = $state<HealthResponse | null>(null);
  let serverVersion = $state<VersionResponse | null>(null);
  let error = $state<ApiError | null>(null);
  let loading = $state(true);

  async function refresh(): Promise<void> {
    loading = true;
    error = null;
    try {
      // Both calls are cheap and independent, so there is no reason to make
      // the operator wait for them in sequence.
      const [nextHealth, nextVersion] = await Promise.all([client.health(), client.version()]);
      health = nextHealth;
      serverVersion = nextVersion;
    } catch (cause) {
      error =
        cause instanceof ApiError
          ? cause
          : new ApiError(String(cause), { code: 'internal', status: 0 });
      // The previous readings are now stale and would be misleading next to a
      // failure, so they are cleared rather than left on screen.
      health = null;
      serverVersion = null;
    } finally {
      loading = false;
    }
  }

  function formatUptime(seconds: number): string {
    if (seconds < 60) return `${seconds}s`;
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes}m`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours}h ${minutes % 60}m`;
    return `${Math.floor(hours / 24)}d ${hours % 24}h`;
  }

  $effect(() => {
    void refresh();
  });
</script>

<div class="mx-auto max-w-4xl px-6 py-10">
  <!-- An unmistakable statement of what this build does and does not do. -->
  <div
    class="mb-10 rounded-lg border border-amber-300 bg-amber-50 p-4 text-sm dark:border-amber-900 dark:bg-amber-950/40"
  >
    <p class="font-medium text-amber-900 dark:text-amber-200">Early development build</p>
    <p class="mt-1 text-amber-900/90 dark:text-amber-200/90">
      Accounts, devices and addressing work. <strong>No VPN functionality exists yet</strong>
      &mdash; no keys are managed for you and no traffic is carried between devices. Nothing on this page
      is simulated: every value comes from a real endpoint.
    </p>
  </div>

  <section class="mb-10" aria-labelledby="status-heading">
    <div class="mb-3 flex items-center justify-between">
      <h2 id="status-heading" class="text-lg font-medium">Control plane</h2>
      <button
        type="button"
        onclick={() => void refresh()}
        disabled={loading}
        class="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium hover:bg-slate-100 disabled:opacity-50 dark:border-slate-700 dark:hover:bg-slate-800"
      >
        {loading ? 'Checking…' : 'Refresh'}
      </button>
    </div>

    <div
      class="rounded-lg border border-slate-200 bg-white p-5 dark:border-slate-800 dark:bg-slate-900"
    >
      {#if loading && !health && !error}
        <p class="text-sm text-slate-600 dark:text-slate-400">Contacting the control server…</p>
      {:else if error}
        <div class="flex items-start gap-3">
          <StatusBadge status="down" />
          <div class="min-w-0">
            <p class="text-sm font-medium">{error.message}</p>
            {#if error.requestId}
              <p class="mt-1 font-mono text-xs text-slate-500 dark:text-slate-400">
                Request ID: {error.requestId}
              </p>
            {/if}
          </div>
        </div>
      {:else if health}
        <div class="flex flex-wrap items-center gap-3">
          <StatusBadge status={health.status} />
          <span class="text-sm text-slate-600 dark:text-slate-400">
            up {formatUptime(health.uptime_seconds)}
          </span>
        </div>

        <dl class="mt-5 grid grid-cols-1 gap-x-8 gap-y-3 text-sm sm:grid-cols-2">
          <div
            class="flex justify-between gap-4 border-b border-slate-100 pb-2 dark:border-slate-800"
          >
            <dt class="text-slate-600 dark:text-slate-400">Server version</dt>
            <dd class="font-mono">{serverVersion?.version ?? health.version}</dd>
          </div>
          <div
            class="flex justify-between gap-4 border-b border-slate-100 pb-2 dark:border-slate-800"
          >
            <dt class="text-slate-600 dark:text-slate-400">Build</dt>
            <dd class="truncate font-mono">{serverVersion?.commit ?? 'unknown'}</dd>
          </div>
          <div
            class="flex justify-between gap-4 border-b border-slate-100 pb-2 dark:border-slate-800"
          >
            <dt class="text-slate-600 dark:text-slate-400">Protocol</dt>
            <dd class="font-mono">v{serverVersion?.protocol_version ?? '?'}</dd>
          </div>
          <div
            class="flex justify-between gap-4 border-b border-slate-100 pb-2 dark:border-slate-800"
          >
            <dt class="text-slate-600 dark:text-slate-400">Capabilities</dt>
            <dd class="text-right font-mono">
              {serverVersion?.capabilities.join(', ') || 'none'}
            </dd>
          </div>
        </dl>

        <h3 class="mt-6 mb-2 text-sm font-medium">Dependencies</h3>
        <ul class="space-y-2">
          {#each health.checks as check (check.name)}
            <li class="flex items-center justify-between gap-4 text-sm">
              <span class="flex items-center gap-2">
                <StatusBadge status={check.status} />
                <span>{check.name}</span>
              </span>
              <span class="text-slate-500 dark:text-slate-400">
                {check.detail ?? `${check.duration_ms}ms`}
              </span>
            </li>
          {:else}
            <li class="text-sm text-slate-500 dark:text-slate-400">No dependencies reported.</li>
          {/each}
        </ul>
      {/if}
    </div>
  </section>

  <section aria-labelledby="planned-heading">
    <h2 id="planned-heading" class="mb-1 text-lg font-medium">Not built yet</h2>
    <p class="mb-4 text-sm text-slate-600 dark:text-slate-400">
      These areas have no screens yet. They are listed rather than shown as empty tables, so that
      nothing here can be mistaken for a working feature. A few already work over the API and are
      marked as such &mdash; claiming those were missing would be just as inaccurate as the reverse.
    </p>

    <ul class="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {#each PLANNED_AREAS as area (area.name)}
        <li class="rounded-lg border border-dashed border-slate-300 p-4 dark:border-slate-700">
          <div class="flex items-baseline justify-between gap-3">
            <span class="font-medium text-slate-700 dark:text-slate-300">{area.name}</span>
            <span
              class="shrink-0 rounded px-2 py-0.5 text-xs font-medium {area.status === 'api-only'
                ? 'bg-sky-100 text-sky-800 dark:bg-sky-950 dark:text-sky-300'
                : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400'}"
            >
              {area.status === 'api-only' ? 'API only' : area.phase}
            </span>
          </div>
          <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">{area.summary}</p>
        </li>
      {/each}
    </ul>
  </section>
</div>
