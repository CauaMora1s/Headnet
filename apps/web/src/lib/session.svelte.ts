/**
 * Authentication state for the whole application.
 *
 * One store, seeded once at start-up, so that every screen agrees on who is
 * signed in. The alternative — each page asking independently — produces a UI
 * that shows a signed-in shell around a page that just got a 401.
 */
import { ApiError, HeadnetClient, type Session, type User } from './api';

/** What the application knows about the current visitor. */
export type AuthPhase =
  /** Still asking the server. Render nothing decisive yet. */
  | 'loading'
  /** No account exists; the installation is waiting to be claimed. */
  | 'bootstrap-required'
  /** An account exists but this visitor is not signed in. */
  | 'signed-out'
  | 'signed-in'
  /** The server could not be reached at all. */
  | 'unreachable';

class SessionStore {
  phase = $state<AuthPhase>('loading');
  user = $state<User | null>(null);
  /** The last error worth showing, if the server could not be reached. */
  error = $state<ApiError | null>(null);

  readonly #client: HeadnetClient;

  constructor(client: HeadnetClient) {
    this.#client = client;
  }

  get signedIn(): boolean {
    return this.phase === 'signed-in' && this.user !== null;
  }

  get isAdmin(): boolean {
    return this.user?.role === 'admin';
  }

  /**
   * Works out where the visitor stands.
   *
   * Asks whether the installation needs claiming first: on a fresh server
   * there is no session to look for, and asking for one would produce a
   * pointless 401 on every load.
   */
  async refresh(): Promise<void> {
    this.error = null;
    try {
      const status = await this.#client.authStatus();
      if (status.bootstrap_required) {
        this.phase = 'bootstrap-required';
        this.user = null;
        return;
      }
    } catch (cause) {
      this.#recordFailure(cause);
      return;
    }

    try {
      const session = await this.#client.session();
      this.adopt(session);
    } catch (cause) {
      if (cause instanceof ApiError && cause.unauthenticated) {
        this.phase = 'signed-out';
        this.user = null;
        return;
      }
      this.#recordFailure(cause);
    }
  }

  /** Records a successful sign-in or first-run claim. */
  adopt(session: Session): void {
    this.user = session.user;
    this.phase = 'signed-in';
    this.error = null;
  }

  /**
   * Drops local state after signing out, or after any request reports that the
   * session has ended.
   *
   * Leaving a stale signed-in shell around a session the server has already
   * discarded is worse than a redirect: it invites someone to keep working in
   * a UI where nothing they do will save.
   */
  clear(): void {
    this.user = null;
    this.phase = 'signed-out';
  }

  /** Called by any screen that receives a 401, to keep the shell honest. */
  handleUnauthenticated(error: unknown): boolean {
    if (error instanceof ApiError && error.unauthenticated) {
      this.clear();
      return true;
    }
    return false;
  }

  #recordFailure(cause: unknown): void {
    this.phase = 'unreachable';
    this.user = null;
    this.error =
      cause instanceof ApiError
        ? cause
        : new ApiError(String(cause), { code: 'internal', status: 0 });
  }
}

/** The client every screen shares. Same-origin, so no base URL is needed. */
export const client = new HeadnetClient();

export const session = new SessionStore(client);

/** Exported for tests, which need their own client. */
export function createSessionStore(withClient: HeadnetClient): SessionStore {
  return new SessionStore(withClient);
}

export type { SessionStore };
