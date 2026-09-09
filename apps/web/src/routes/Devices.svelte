<script lang="ts">
  import { ApiError, type Device } from '../lib/api';
  import { canAuthenticate, countWithoutCredential } from '../lib/devices';
  import { client, session } from '../lib/session.svelte';

  let devices = $state<Device[]>([]);
  let loading = $state(true);
  let error = $state<ApiError | null>(null);

  // The device being revoked, held while the confirmation is open. Revocation
  // removes a machine from a network that may be the only way to reach it, so
  // it is never one click.
  let pendingRevoke = $state<Device | null>(null);
  let working = $state(false);

  let showRegister = $state(false);
  let newName = $state('');
  let newPublicKey = $state('');
  let registerError = $state<ApiError | null>(null);

  async function load() {
    loading = true;
    error = null;
    try {
      devices = await client.devices();
    } catch (cause) {
      if (!session.handleUnauthenticated(cause)) {
        error = asApiError(cause);
      }
    } finally {
      loading = false;
    }
  }

  async function register(event: SubmitEvent) {
    event.preventDefault();
    working = true;
    registerError = null;
    try {
      await client.registerDevice({ name: newName, public_key: newPublicKey });
      newName = '';
      newPublicKey = '';
      showRegister = false;
      await load();
    } catch (cause) {
      if (!session.handleUnauthenticated(cause)) {
        registerError = asApiError(cause);
      }
    } finally {
      working = false;
    }
  }

  async function confirmRevoke() {
    if (!pendingRevoke) return;
    working = true;
    try {
      await client.revokeDevice(pendingRevoke.id);
      pendingRevoke = null;
      await load();
    } catch (cause) {
      if (!session.handleUnauthenticated(cause)) {
        error = asApiError(cause);
      }
      pendingRevoke = null;
    } finally {
      working = false;
    }
  }

  function asApiError(cause: unknown): ApiError {
    return cause instanceof ApiError
      ? cause
      : new ApiError(String(cause), { code: 'internal', status: 0 });
  }

  const active = $derived(devices.filter((d) => !d.revoked_at));
  const revoked = $derived(devices.filter((d) => d.revoked_at));

  const inventoryOnly = $derived(countWithoutCredential(devices));

  $effect(() => {
    void load();
  });
</script>

<div class="mx-auto max-w-4xl px-6 py-10">
  <div class="mb-2 flex items-center justify-between">
    <h1 class="text-2xl font-semibold tracking-tight">Devices</h1>
    <button
      type="button"
      onclick={() => (showRegister = !showRegister)}
      class="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium hover:bg-slate-100 dark:border-slate-700 dark:hover:bg-slate-800"
    >
      {showRegister ? 'Cancel' : 'Add a device'}
    </button>
  </div>

  <!-- Nothing carries traffic between these devices yet. A page that looked
       like a working VPN would be exactly the impression this project refuses
       to give. -->
  <p
    class="mb-6 rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-200"
  >
    <strong>No traffic is carried yet.</strong> Devices here have an address reserved, and an enrolled
    one can authenticate to this server &mdash; but nothing connects them to each other. Peer connectivity
    arrives in Phase 3.
  </p>

  {#if inventoryOnly > 0}
    <p
      class="mb-6 rounded-lg border border-slate-300 bg-slate-50 p-3 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300"
    >
      {inventoryOnly}
      {inventoryOnly === 1 ? 'device is' : 'devices are'} recorded here without a credential, so
      {inventoryOnly === 1 ? 'it cannot' : 'they cannot'} contact this server even once Phase 3 lands.
      Enrol with a setup key to give a machine one.
    </p>
  {/if}

  {#if showRegister}
    <form
      onsubmit={register}
      class="mb-8 space-y-4 rounded-lg border border-slate-200 bg-white p-5 dark:border-slate-800 dark:bg-slate-900"
    >
      <!-- The most important text on this page. Without it the form looks like
           the way to put a machine on the network, and it is not. -->
      <div
        class="rounded-md border border-slate-200 bg-slate-50 p-3 text-sm dark:border-slate-700 dark:bg-slate-950"
      >
        <p class="font-medium">This records a device. It does not let it connect.</p>
        <p class="mt-1 text-slate-600 dark:text-slate-400">
          The machine gets an address reserved and its public key on file, but no credential, so it
          cannot contact this server. Use this to reserve an address, or to note a device you will
          enrol properly later.
        </p>
        <p class="mt-2 text-slate-600 dark:text-slate-400">
          A machine that will actually join the network <strong>enrols itself</strong> with a setup key
          and receives its credential then. Setup keys are created over the API today; their screens are
          still to come.
        </p>
      </div>

      <div>
        <label for="name" class="block text-sm font-medium">Name</label>
        <input
          id="name"
          bind:value={newName}
          required
          maxlength="64"
          placeholder="laptop"
          class="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-700 dark:bg-slate-950"
        />
      </div>

      <div>
        <label for="public-key" class="block text-sm font-medium">Public key</label>
        <input
          id="public-key"
          bind:value={newPublicKey}
          required
          spellcheck="false"
          placeholder="base64, 44 characters"
          aria-describedby="key-help"
          class="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 font-mono text-sm dark:border-slate-700 dark:bg-slate-950"
        />
        <p id="key-help" class="mt-1 text-xs text-slate-500 dark:text-slate-400">
          The device's <strong>public</strong> key. Its private key is generated on the device and
          must never leave it &mdash; there is nowhere here to put one, deliberately. The server
          knows how to generate and store a key locally, but no client daemon exposes it yet, so for
          now this comes from
          <code class="font-mono text-[0.7rem]">wg genkey | wg pubkey</code>.
        </p>
      </div>

      {#if registerError}
        <p
          role="alert"
          class="rounded-md bg-rose-50 px-3 py-2 text-sm text-rose-900 dark:bg-rose-950 dark:text-rose-200"
        >
          {registerError.message}
        </p>
      {/if}

      <button
        type="submit"
        disabled={working}
        class="rounded-md bg-slate-900 px-3 py-2 text-sm font-medium text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
      >
        {working ? 'Registering…' : 'Register device'}
      </button>
    </form>
  {/if}

  {#if loading}
    <p class="text-sm text-slate-600 dark:text-slate-400">Loading devices…</p>
  {:else if error}
    <p
      role="alert"
      class="rounded-md bg-rose-50 px-3 py-2 text-sm text-rose-900 dark:bg-rose-950 dark:text-rose-200"
    >
      {error.message}
      {#if error.requestId}
        <span class="mt-1 block font-mono text-xs opacity-75">Request ID: {error.requestId}</span>
      {/if}
    </p>
  {:else if devices.length === 0}
    <div
      class="rounded-lg border border-dashed border-slate-300 p-8 text-center dark:border-slate-700"
    >
      <p class="font-medium">No devices yet</p>
      <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
        Create a setup key over the API and let the machine enrol itself &mdash; that is the path
        that produces a device able to connect. Adding one above records it without a credential.
      </p>
    </div>
  {:else}
    <div class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead
          class="border-b border-slate-200 text-slate-600 dark:border-slate-800 dark:text-slate-400"
        >
          <tr>
            <th scope="col" class="py-2 pr-4 font-medium">Name</th>
            <th scope="col" class="py-2 pr-4 font-medium">Address</th>
            <th scope="col" class="py-2 pr-4 font-medium">Credential</th>
            <th scope="col" class="py-2 pr-4 font-medium">OS</th>
            {#if session.isAdmin}
              <th scope="col" class="py-2 pr-4 font-medium">Owner</th>
            {/if}
            <th scope="col" class="py-2 pr-4 font-medium">Added</th>
            <th scope="col" class="py-2 font-medium"><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {#each active as device (device.id)}
            <tr class="border-b border-slate-100 dark:border-slate-800/60">
              <td class="py-3 pr-4 font-medium">{device.name}</td>
              <td class="py-3 pr-4 font-mono text-xs">{device.ipv4 ?? '—'}</td>
              <td class="py-3 pr-4">
                <!-- The label carries the meaning, not the colour. -->
                {#if canAuthenticate(device)}
                  <span
                    class="inline-flex items-center rounded-full bg-emerald-100 px-2.5 py-0.5 text-xs font-medium text-emerald-900 dark:bg-emerald-950 dark:text-emerald-200"
                    title="Enrolled with a setup key, so this device holds a credential and can authenticate."
                  >
                    Enrolled
                  </span>
                {:else}
                  <span
                    class="inline-flex items-center rounded-full bg-slate-200 px-2.5 py-0.5 text-xs font-medium text-slate-700 dark:bg-slate-800 dark:text-slate-300"
                    title="Registered without a setup key, so this device has no credential and cannot contact the server."
                  >
                    Record only
                  </span>
                {/if}
              </td>
              <td class="py-3 pr-4 text-slate-600 dark:text-slate-400">{device.os || '—'}</td>
              {#if session.isAdmin}
                <td class="py-3 pr-4 font-mono text-xs text-slate-500">{device.user_id}</td>
              {/if}
              <td class="py-3 pr-4 text-slate-600 dark:text-slate-400">
                {new Date(device.created_at).toLocaleDateString()}
              </td>
              <td class="py-3 text-right">
                <button
                  type="button"
                  onclick={() => (pendingRevoke = device)}
                  class="rounded-md border border-rose-300 px-2 py-1 text-xs font-medium text-rose-700 hover:bg-rose-50 dark:border-rose-900 dark:text-rose-300 dark:hover:bg-rose-950"
                >
                  Revoke
                </button>
              </td>
            </tr>
          {/each}

          {#each revoked as device (device.id)}
            <tr class="border-b border-slate-100 opacity-50 dark:border-slate-800/60">
              <td class="py-3 pr-4 font-medium line-through">{device.name}</td>
              <td class="py-3 pr-4 text-xs">address released</td>
              <td class="py-3 pr-4 text-xs">credential deleted</td>
              <td class="py-3 pr-4">{device.os || '—'}</td>
              {#if session.isAdmin}
                <td class="py-3 pr-4 font-mono text-xs">{device.user_id}</td>
              {/if}
              <td class="py-3 pr-4">{new Date(device.created_at).toLocaleDateString()}</td>
              <td class="py-3 text-right text-xs">Revoked</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

{#if pendingRevoke}
  <!-- Naming the device in the confirmation is the point: a mis-clicked revoke
       removes a machine from a network that may be the only way to reach it. -->
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 p-4"
    role="dialog"
    aria-modal="true"
    aria-labelledby="revoke-title"
  >
    <div class="w-full max-w-md rounded-lg bg-white p-6 dark:bg-slate-900">
      <h2 id="revoke-title" class="text-lg font-medium">Revoke {pendingRevoke.name}?</h2>
      <p class="mt-2 text-sm text-slate-600 dark:text-slate-400">
        It will be removed from the network and its address
        <span class="font-mono">{pendingRevoke.ipv4 ?? ''}</span>
        returned to the pool. Its public key stays blocked permanently, so the device has to generate
        a new one to rejoin. This cannot be undone.
      </p>
      {#if canAuthenticate(pendingRevoke)}
        <!-- The address returning to the pool is the visible half. The credential
             is the half that actually stops the machine talking to this server,
             and a confirmation that omits it understates what is about to
             happen. -->
        <p class="mt-2 text-sm text-slate-600 dark:text-slate-400">
          Its credential is destroyed at the same moment, so the machine stops being able to reach
          this server straight away. It will need a new setup key to come back.
        </p>
      {/if}
      <div class="mt-6 flex justify-end gap-3">
        <button
          type="button"
          onclick={() => (pendingRevoke = null)}
          class="rounded-md border border-slate-300 px-3 py-2 text-sm font-medium dark:border-slate-700"
        >
          Cancel
        </button>
        <button
          type="button"
          onclick={confirmRevoke}
          disabled={working}
          class="rounded-md bg-rose-600 px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
        >
          {working ? 'Revoking…' : 'Revoke device'}
        </button>
      </div>
    </div>
  </div>
{/if}
