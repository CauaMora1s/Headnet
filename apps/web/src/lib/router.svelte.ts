/**
 * Reactive navigation state, built on the pure matching in router.ts.
 *
 * Kept separate from the matching itself so the logic can be tested without a
 * DOM or a Svelte runtime.
 */
import { FALLBACK, matchRoute, type Route } from './router';

class Router {
  /** The route currently displayed. */
  current = $state<Route>(FALLBACK);

  start(): () => void {
    this.current = matchRoute(window.location.pathname);

    // The back and forward buttons have to work, or this is not a web page.
    const onPopState = () => {
      this.current = matchRoute(window.location.pathname);
    };
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }

  /** Navigates without a page load. */
  navigate(to: Route): void {
    if (this.current === to && window.location.pathname === to) return;
    window.history.pushState({}, '', to);
    this.current = to;
  }

  /**
   * Navigates without adding a history entry.
   *
   * Used for redirects the user did not ask for — being bounced to the sign-in
   * page, say — so that pressing back does not return them to a screen they
   * were never allowed to see.
   */
  replace(to: Route): void {
    window.history.replaceState({}, '', to);
    this.current = to;
  }
}

export const router = new Router();
