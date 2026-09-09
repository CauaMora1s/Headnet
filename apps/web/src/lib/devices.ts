import type { Device } from './api';

/**
 * Whether a device holds a credential it can authenticate with.
 *
 * There are two ways a device gets into the list and only one of them produces
 * a machine that can talk to the control plane. Enrolling with a setup key
 * mints a bearer credential, returned once, which the device presents on every
 * later request. Registering through the web UI does not: it records a public
 * key and reserves an address, and the machine has nothing to authenticate
 * with.
 *
 * Both are honest operations, but they are not interchangeable, and the table
 * showed them identically until this existed — so an operator could reasonably
 * conclude the difference was one of convenience.
 *
 * `enrolled_with` is the signal because the credential itself is never returned
 * by any read: only a device that came in through a setup key has one.
 * Revocation deletes the credential while keeping the record, which is why a
 * revoked device never counts as connectable.
 *
 * This is deliberately a pure function rather than a method on the page, so
 * that the rule can be tested without a DOM or a Svelte runtime — the same
 * split as `matchRoute` and the reactive router.
 */
export function canAuthenticate(device: Device): boolean {
  return Boolean(device.enrolled_with) && !device.revoked_at;
}

/**
 * How many live devices have no credential.
 *
 * Revoked devices are excluded: they have no credential either, but saying so
 * would be noise — an operator already knows a revoked device is gone, and
 * counting it here would make the number look alarming for no reason.
 */
export function countWithoutCredential(devices: readonly Device[]): number {
  return devices.filter((device) => !device.revoked_at && !canAuthenticate(device)).length;
}
