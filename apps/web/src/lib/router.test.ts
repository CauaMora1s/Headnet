import { describe, expect, it } from 'vitest';

import { FALLBACK, matchRoute, ROUTES } from './router';

describe('matchRoute', () => {
  it('matches every declared route', () => {
    for (const route of ROUTES) {
      expect(matchRoute(route)).toBe(route);
    }
  });

  it('tolerates a trailing slash, because people type them', () => {
    expect(matchRoute('/devices/')).toBe('/devices');
    expect(matchRoute('/login/')).toBe('/login');
    // The root is already a single slash and must not become empty.
    expect(matchRoute('/')).toBe('/');
  });

  it('falls back rather than rendering nothing', () => {
    // A blank screen is the worst possible answer to a mistyped URL.
    for (const path of ['/nope', '/devices/extra', '/DEVICES', '', '/api/v1/devices']) {
      expect(matchRoute(path)).toBe(FALLBACK);
    }
  });

  it('does not treat a path prefix as a match', () => {
    expect(matchRoute('/devicesfoo')).toBe(FALLBACK);
    expect(matchRoute('/login2')).toBe(FALLBACK);
  });
});
