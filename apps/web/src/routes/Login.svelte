<script lang="ts">
  import { ApiError } from '../lib/api';
  import { client, session } from '../lib/session.svelte';
  import { router } from '../lib/router.svelte';

  let email = $state('');
  let password = $state('');
  let submitting = $state(false);
  let error = $state<ApiError | null>(null);

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    submitting = true;
    error = null;
    try {
      session.adopt(await client.login(email, password));
      // Replace rather than push: the sign-in page is not somewhere the back
      // button should return a signed-in user to.
      router.replace('/');
    } catch (cause) {
      error =
        cause instanceof ApiError
          ? cause
          : new ApiError(String(cause), {
              code: 'internal',
              status: 0,
            });
      password = '';
    } finally {
      submitting = false;
    }
  }
</script>

<div class="mx-auto max-w-sm px-6 py-16">
  <h1 class="text-2xl font-semibold tracking-tight">Sign in</h1>
  <p class="mt-1 mb-8 text-sm text-slate-600 dark:text-slate-400">to your Headnet network</p>

  <form onsubmit={submit} class="space-y-4">
    <div>
      <label for="email" class="block text-sm font-medium">Email</label>
      <input
        id="email"
        type="email"
        bind:value={email}
        required
        autocomplete="username"
        class="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
      />
    </div>

    <div>
      <label for="password" class="block text-sm font-medium">Password</label>
      <input
        id="password"
        type="password"
        bind:value={password}
        required
        autocomplete="current-password"
        class="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
      />
    </div>

    {#if error}
      <!-- The server deliberately returns the same message whether the address
           is unknown or the password is wrong, so this shows it verbatim
           rather than guessing at something more specific. -->
      <p
        role="alert"
        class="rounded-md bg-rose-50 px-3 py-2 text-sm text-rose-900 dark:bg-rose-950 dark:text-rose-200"
      >
        {error.message}
        {#if error.requestId}
          <span class="mt-1 block font-mono text-xs opacity-75">Request ID: {error.requestId}</span>
        {/if}
      </p>
    {/if}

    <button
      type="submit"
      disabled={submitting}
      class="w-full rounded-md bg-slate-900 px-3 py-2 font-medium text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
    >
      {submitting ? 'Signing in…' : 'Sign in'}
    </button>
  </form>
</div>
