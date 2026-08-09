// Authorization Code + PKCE, browser side (spec §9).
//
// PKCE is what makes a public client safe: the code that comes back through
// the redirect is useless without the verifier, which never leaves this tab.

const VERIFIER_CHARS =
  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~";

/** Cryptographically random string from the unreserved character set. */
function randomString(length: number): string {
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);

  // Modulo over 64 characters divides 256 evenly, so no value is favoured.
  return Array.from(bytes, (b) => VERIFIER_CHARS[b % VERIFIER_CHARS.length]).join("");
}

function base64url(bytes: ArrayBuffer): string {
  const binary = String.fromCharCode(...new Uint8Array(bytes));
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** The S256 transform the server and Google both expect. */
export async function challengeFor(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
  return base64url(digest);
}

export interface PendingLogin {
  /** Proves this tab started the flow; checked when the provider returns. */
  state: string;
  /** Echoed in the ID token, and checked server-side against what we send. */
  nonce: string;
  /** Never sent to the provider — only to our own callback endpoint. */
  verifier: string;
}

/**
 * 43 characters is the RFC 7636 minimum for a verifier; the state and nonce
 * only need to be unguessable.
 */
export function newPendingLogin(): PendingLogin {
  return {
    state: randomString(32),
    nonce: randomString(32),
    verifier: randomString(64),
  };
}
