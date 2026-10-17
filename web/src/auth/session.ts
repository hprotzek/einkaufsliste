import type { components } from "../api/schema";

import { challengeFor, newPendingLogin, type PendingLogin } from "./pkce";

type User = components["schemas"]["User"];
type SessionResponse = components["schemas"]["Session"];

/**
 * The access token lives here and nowhere else: a module variable, wiped on
 * reload. Never localStorage, never sessionStorage, never a cookie readable
 * by script (non-negotiable 6). The refresh token is an HttpOnly cookie this
 * code cannot see at all, which is the point.
 */
let accessToken: string | null = null;

/** The in-flight refresh, if any. See refresh() for why this exists. */
let refreshing: Promise<string | null> | null = null;

/**
 * The pending login survives the redirect to Google, so it has to be written
 * somewhere. sessionStorage rather than localStorage: it is scoped to this
 * tab and cleared when the tab closes, and it holds no credential — the
 * verifier is worthless without the matching code, which only arrives back
 * in this same tab.
 */
const PENDING_KEY = "einkaufsliste.pending-login";

export const CALLBACK_PATH = "/auth/callback";

/** The provider name in the API path. Never assumed elsewhere (non-negotiable 10). */
const PROVIDER = "google";

function redirectURI(): string {
  return window.location.origin + CALLBACK_PATH;
}

function clientID(): string {
  const id = import.meta.env.VITE_GOOGLE_CLIENT_ID;
  if (!id) {
    throw new Error(
      "VITE_GOOGLE_CLIENT_ID is not set; sign-in is not configured for this build",
    );
  }
  return id;
}

/** Sends the browser to Google. Does not return. */
export async function beginSignIn(): Promise<void> {
  const pending = newPendingLogin();
  sessionStorage.setItem(PENDING_KEY, JSON.stringify(pending));

  const params = new URLSearchParams({
    client_id: clientID(),
    redirect_uri: redirectURI(),
    response_type: "code",
    scope: "openid email profile",
    state: pending.state,
    nonce: pending.nonce,
    code_challenge: await challengeFor(pending.verifier),
    code_challenge_method: "S256",
    // Without this Google skips the account chooser for a single signed-in
    // account, which is confusing on a shared family device.
    prompt: "select_account",
  });

  window.location.assign(`https://accounts.google.com/o/oauth2/v2/auth?${params}`);
}

function takePendingLogin(): PendingLogin | null {
  const raw = sessionStorage.getItem(PENDING_KEY);
  sessionStorage.removeItem(PENDING_KEY);
  if (!raw) {
    return null;
  }

  try {
    return JSON.parse(raw) as PendingLogin;
  } catch {
    return null;
  }
}

/**
 * Completes the flow after Google redirects back. The code is posted to our
 * own server, which exchanges it — the provider's tokens never reach this
 * code (§9).
 */
export async function completeSignIn(search: string): Promise<User> {
  const params = new URLSearchParams(search);

  const error = params.get("error");
  if (error) {
    throw new Error(`sign-in was refused: ${error}`);
  }

  const code = params.get("code");
  const state = params.get("state");
  const pending = takePendingLogin();

  if (!code || !state || !pending) {
    throw new Error("this sign-in did not start in this tab");
  }
  // A mismatched state means the redirect did not come from the request this
  // tab made, which is the CSRF case the parameter exists for.
  if (state !== pending.state) {
    throw new Error("this sign-in did not start in this tab");
  }

  const response = await fetch(`/api/v1/auth/oidc/${PROVIDER}/callback`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      code,
      code_verifier: pending.verifier,
      redirect_uri: redirectURI(),
      nonce: pending.nonce,
    }),
  });

  if (!response.ok) {
    throw new Error("sign-in failed");
  }

  const session = (await response.json()) as SessionResponse;
  accessToken = session.access_token;

  return session.user;
}

/**
 * Every tab of this app shares one refresh cookie, so a refresh in one tab
 * must not overlap a refresh in another. The name is per-origin: the lock is
 * only ever contended by tabs of this same app.
 */
const REFRESH_LOCK = "einkaufsliste.auth-refresh";

/**
 * A refresh that never answers would otherwise hold the cross-tab lock
 * forever, and every tab of the app would wait behind it.
 */
const REFRESH_TIMEOUT_MS = 15_000;

/**
 * How long to wait before the one retry after a 401. A tab that lost a race
 * to another tab gets a 401 while the winner's response is still landing;
 * by the time this has passed, the shared cookie holds the winner's
 * successor and the retry rotates that one (spec §9.2).
 */
const RACE_RETRY_DELAY_MS = 500;

async function postRefreshOnce(): Promise<Response> {
  return fetch("/api/v1/auth/refresh", {
    method: "POST",
    signal: AbortSignal.timeout(REFRESH_TIMEOUT_MS),
  });
}

async function postRefresh(): Promise<string | null> {
  try {
    let response = await postRefreshOnce();

    // One retry, never more: a second 401 means the session really is over,
    // and looping would only hammer the server with a dead cookie.
    if (response.status === 401) {
      await new Promise((resolve) => setTimeout(resolve, RACE_RETRY_DELAY_MS));
      response = await postRefreshOnce();
    }

    if (!response.ok) {
      accessToken = null;
      return null;
    }

    const session = (await response.json()) as SessionResponse;
    accessToken = session.access_token;
    return accessToken;
  } catch {
    // Timed out or offline. The session may well be fine; there is simply
    // no fresh access token right now, and the next request will try again.
    accessToken = null;
    return null;
  }
}

/**
 * Exchanges the refresh cookie for a new access token.
 *
 * Serialised at two levels, because rotation means a second refresh with the
 * same cookie presents a token the first has already spent:
 *
 * - Within this tab, single-flight: several requests noticing a 401 at once
 *   share one refresh.
 * - Across tabs, a Web Lock: the installed app and a browser tab, or two tabs
 *   restored together, take turns. The second waits, then sends the cookie
 *   the first has just replaced, and rotates that one cleanly (spec §9.2).
 *
 * Web Locks exist only in a secure context. A local stack on plain HTTP
 * reached by LAN address has none, and falls back to tab-local single-flight;
 * the server's grace window still keeps a lost race from signing anyone out.
 */
export function refresh(): Promise<string | null> {
  refreshing ??= (async () => {
    try {
      const locks = globalThis.navigator?.locks;
      return locks ? await locks.request(REFRESH_LOCK, postRefresh) : await postRefresh();
    } finally {
      // Cleared whatever happened, so the next 401 starts a fresh attempt
      // rather than resolving against a stale promise.
      refreshing = null;
    }
  })();

  return refreshing;
}

/**
 * fetch for the API, with the access token attached and one retry after a
 * refresh. Anything still unauthorised means the session is over.
 */
export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const send = (token: string | null): Promise<Response> =>
    fetch(path, {
      ...init,
      headers: {
        ...init.headers,
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
    });

  let response = await send(accessToken);
  if (response.status !== 401) {
    return response;
  }

  const renewed = await refresh();
  if (!renewed) {
    return response;
  }

  response = await send(renewed);
  return response;
}

/** Loads the signed-in user, or null when there is no session. */
export async function loadMe(): Promise<User | null> {
  const response = await apiFetch("/api/v1/me");
  if (!response.ok) {
    return null;
  }
  return (await response.json()) as User;
}

export async function signOut(): Promise<void> {
  accessToken = null;
  await fetch("/api/v1/auth/logout", { method: "POST" });
}

/**
 * Restores a session on load. The access token is gone after a reload by
 * design, but the refresh cookie is not, so a returning visitor gets a
 * session back without touching the provider.
 */
export async function restore(): Promise<User | null> {
  if (!(await refresh())) {
    return null;
  }
  return loadMe();
}
