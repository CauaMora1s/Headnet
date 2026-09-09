import { describe, expect, it } from 'vitest';

import type { Device } from './api';
import { canAuthenticate, countWithoutCredential } from './devices';

function device(overrides: Partial<Device> = {}): Device {
  return {
    id: 'dev_1',
    user_id: 'usr_1',
    name: 'laptop',
    public_key: 'A'.repeat(43) + '=',
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

describe('canAuthenticate', () => {
  it('is true only for a device enrolled with a setup key', () => {
    // The whole point of the distinction: a device registered through the web
    // UI has no credential and never will, until it re-enrols. Showing the two
    // identically invited the reader to assume they differed only in
    // convenience.
    expect(canAuthenticate(device({ enrolled_with: 'sk_1' }))).toBe(true);
    expect(canAuthenticate(device())).toBe(false);
  });

  it('is false once the device is revoked', () => {
    // Revocation deletes the credential but keeps the record, so
    // enrolled_with survives. Reading it alone would report a revoked device
    // as able to authenticate, which is the opposite of what revocation means.
    expect(
      canAuthenticate(device({ enrolled_with: 'sk_1', revoked_at: '2026-02-01T00:00:00Z' })),
    ).toBe(false);
  });

  it('treats an empty enrolled_with as absent', () => {
    // omitempty means the field is normally missing rather than empty, but a
    // hand-written client or a future serialiser could send "".
    expect(canAuthenticate(device({ enrolled_with: '' }))).toBe(false);
  });
});

describe('countWithoutCredential', () => {
  it('counts live devices that cannot authenticate', () => {
    expect(
      countWithoutCredential([
        device({ id: 'a', enrolled_with: 'sk_1' }),
        device({ id: 'b' }),
        device({ id: 'c' }),
      ]),
    ).toBe(2);
  });

  it('ignores revoked devices', () => {
    // A revoked device has no credential either, but the operator already
    // knows it is gone. Counting it would make the warning look alarming for
    // no reason, and a warning that cries wolf gets ignored when it matters.
    expect(
      countWithoutCredential([
        device({ id: 'a', revoked_at: '2026-02-01T00:00:00Z' }),
        device({ id: 'b', enrolled_with: 'sk_1', revoked_at: '2026-02-01T00:00:00Z' }),
        device({ id: 'c', enrolled_with: 'sk_1' }),
      ]),
    ).toBe(0);
  });

  it('is zero for an empty list', () => {
    expect(countWithoutCredential([])).toBe(0);
  });
});
