/**
 * A minimal History API router.
 *
 * Four routes do not justify a dependency, for the same reason the Go side
 * uses the standard library's ServeMux and flag package rather than a router
 * and a CLI framework. See ADR-0010 and ADR-0011.
 *
 * Path matching is exact and there are no parameters: every screen in this UI
 * is addressed by a fixed path, and identifiers travel in component state
 * rather than in the URL. When that stops being true, this is the file to
 * replace.
 */

/** The paths this application serves. */
export const ROUTES = ['/', '/login', '/setup', '/devices'] as const;

export type Route = (typeof ROUTES)[number];

/** The route used when a path matches nothing. */
export const FALLBACK: Route = '/';

/**
 * Resolves a path to a route.
 *
 * A trailing slash is tolerated because people type them, and an unknown path
 * falls back rather than rendering nothing — a blank screen is the worst
 * possible answer to a mistyped URL.
 */
export function matchRoute(pathname: string): Route {
  const normalised = normalise(pathname);
  return (ROUTES as readonly string[]).includes(normalised) ? (normalised as Route) : FALLBACK;
}

function normalise(pathname: string): string {
  if (pathname.length > 1 && pathname.endsWith('/')) {
    return pathname.slice(0, -1);
  }
  return pathname || '/';
}
