<script lang="ts">
  import { client, session } from './lib/session.svelte';
  import { router } from './lib/router.svelte';
  import Dashboard from './routes/Dashboard.svelte';
  import Devices from './routes/Devices.svelte';
  import Login from './routes/Login.svelte';
  import Setup from './routes/Setup.svelte';

  let signingOut = $state(false);

  async function signOut() {
    signingOut = true;
    try {
      await client.logout();
    } catch {
      // The server may already have discarded the session. Either way the
      // local state has to be cleared, or the shell would keep claiming to be
      // signed in.
    } finally {
      session.clear();
      router.replace('/login');
      signingOut = false;
    }
  }

  $effect(() => {
    const stop = router.start();
    void session.refresh();
    return stop;
  });

  // Where the visitor is *allowed* to be, given what the server says. Routing
  // is derived from auth state rather than guarded inside each page, so a new
  // page cannot forget to check.
  const view = $derived.by(() => {
    switch (session.phase) {
      case 'loading':
        return 'loading';
      case 'unreachable':
        return 'unreachable';
      case 'bootstrap-required':
        return 'setup';
      case 'signed-out':
        return 'login';
      case 'signed-in':
        return router.current === '/devices' ? 'devices' : 'dashboard';
    }
  });
</script>

<div class="min-h-screen">
  {#if view === 'loading'}
    <div class="mx-auto max-w-4xl px-6 py-16">
      <p class="text-sm text-slate-600 dark:text-slate-400">Loading…</p>
    </div>
  {:else if view === 'unreachable'}
    <div class="mx-auto max-w-lg px-6 py-16">
      <h1 class="text-xl font-semibold">Cannot reach the control server</h1>
      <p class="mt-2 text-sm text-slate-600 dark:text-slate-400">
        {session.error?.message ?? 'The server did not respond.'}
      </p>
      <p class="mt-4 text-sm text-slate-600 dark:text-slate-400">
        Check that <code class="font-mono">headnet-server</code> is running, and that this page is served
        from the same origin or through the development proxy.
      </p>
      <button
        type="button"
        onclick={() => void session.refresh()}
        class="mt-6 rounded-md border border-slate-300 px-3 py-2 text-sm font-medium dark:border-slate-700"
      >
        Try again
      </button>
    </div>
  {:else if view === 'setup'}
    <Setup />
  {:else if view === 'login'}
    <Login />
  {:else}
    <header class="border-b border-slate-200 dark:border-slate-800">
      <div class="mx-auto flex max-w-4xl items-center justify-between px-6 py-4">
        <div class="flex items-center gap-6">
          <span class="font-semibold tracking-tight">Headnet</span>
          <nav class="flex gap-4 text-sm">
            <button
              type="button"
              onclick={() => router.navigate('/')}
              class="hover:underline {router.current === '/'
                ? 'font-medium'
                : 'text-slate-600 dark:text-slate-400'}"
              aria-current={router.current === '/' ? 'page' : undefined}
            >
              Overview
            </button>
            <button
              type="button"
              onclick={() => router.navigate('/devices')}
              class="hover:underline {router.current === '/devices'
                ? 'font-medium'
                : 'text-slate-600 dark:text-slate-400'}"
              aria-current={router.current === '/devices' ? 'page' : undefined}
            >
              Devices
            </button>
          </nav>
        </div>

        <div class="flex items-center gap-4 text-sm">
          <span class="text-slate-600 dark:text-slate-400">
            {session.user?.email}
            {#if session.isAdmin}
              <span class="ml-1 rounded bg-slate-100 px-1.5 py-0.5 text-xs dark:bg-slate-800">
                admin
              </span>
            {/if}
          </span>
          <button
            type="button"
            onclick={() => void signOut()}
            disabled={signingOut}
            class="rounded-md border border-slate-300 px-3 py-1.5 font-medium hover:bg-slate-100 disabled:opacity-50 dark:border-slate-700 dark:hover:bg-slate-800"
          >
            {signingOut ? 'Signing out…' : 'Sign out'}
          </button>
        </div>
      </div>
    </header>

    {#if view === 'devices'}
      <Devices />
    {:else}
      <Dashboard />
    {/if}
  {/if}
</div>
