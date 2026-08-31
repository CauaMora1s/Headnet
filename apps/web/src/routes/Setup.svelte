<script lang="ts">
  import { ApiError } from '../lib/api';
  import { client, session } from '../lib/session.svelte';
  import { router } from '../lib/router.svelte';

  const MIN_PASSWORD_LENGTH = 12;

  let email = $state('');
  let password = $state('');
  let submitting = $state(false);
  let error = $state<ApiError | null>(null);

  const tooShort = $derived(password.length > 0 && password.length < MIN_PASSWORD_LENGTH);

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    submitting = true;
    error = null;
    try {
      session.adopt(await client.bootstrap(email, password));
      router.replace('/');
    } catch (cause) {
      error =
        cause instanceof ApiError
          ? cause
          : new ApiError(String(cause), {
              code: 'internal',
              status: 0,
            });
    } finally {
      submitting = false;
    }
  }
</script>

<div class="mx-auto max-w-lg px-6 py-16">
  <h1 class="text-2xl font-semibold tracking-tight">Set up this server</h1>
  <p class="mt-1 text-sm text-slate-600 dark:text-slate-400">
    Nobody has claimed this Headnet server yet. The account you create here is its administrator.
  </p>

  <!-- The window this form is open in is a real risk, and the server warns
       about it on every start. Saying so here too is the least the UI can do. -->
  <div
    class="my-6 rounded-lg border border-amber-300 bg-amber-50 p-4 text-sm dark:border-amber-900 dark:bg-amber-950/40"
  >
    <p class="font-medium text-amber-900 dark:text-amber-200">Claim it now</p>
    <p class="mt-1 text-amber-900/90 dark:text-amber-200/90">
      Until you do, anyone who can reach this server can claim it and become the administrator of
      your network. Do not expose it publicly before finishing this step.
    </p>
  </div>

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
        minlength={MIN_PASSWORD_LENGTH}
        autocomplete="new-password"
        aria-describedby="password-help"
        class="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
      />
      <p id="password-help" class="mt-1 text-xs text-slate-500 dark:text-slate-400">
        At least {MIN_PASSWORD_LENGTH} characters. Length matters far more than punctuation, so a passphrase
        beats something short and cryptic.
      </p>
      {#if tooShort}
        <p class="mt-1 text-xs text-amber-700 dark:text-amber-400">
          {MIN_PASSWORD_LENGTH - password.length} more to go.
        </p>
      {/if}
    </div>

    {#if error}
      <p
        role="alert"
        class="rounded-md bg-rose-50 px-3 py-2 text-sm text-rose-900 dark:bg-rose-950 dark:text-rose-200"
      >
        {error.message}
        {#if error.code === 'conflict'}
          <span class="mt-1 block">Someone has already claimed this server. Sign in instead.</span>
        {/if}
      </p>
    {/if}

    <button
      type="submit"
      disabled={submitting || tooShort}
      class="w-full rounded-md bg-slate-900 px-3 py-2 font-medium text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
    >
      {submitting ? 'Creating…' : 'Create administrator account'}
    </button>
  </form>
</div>
