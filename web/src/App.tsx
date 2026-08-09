import { useEffect, useState } from "react";

import type { components } from "./api/schema";
import {
  beginSignIn,
  CALLBACK_PATH,
  completeSignIn,
  restore,
  signOut,
} from "./auth/session";

type User = components["schemas"]["User"];

type State =
  | { status: "loading" }
  | { status: "signed-out"; error?: string }
  | { status: "signed-in"; user: User };

export function App() {
  const [state, setState] = useState<State>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;

    const settle = (next: State) => {
      if (!cancelled) {
        setState(next);
      }
    };

    const start = async () => {
      // Returning from the provider: finish the exchange, then take the code
      // out of the URL so a reload cannot replay it.
      if (window.location.pathname === CALLBACK_PATH) {
        try {
          const user = await completeSignIn(window.location.search);
          window.history.replaceState(null, "", "/");
          settle({ status: "signed-in", user });
        } catch (error: unknown) {
          window.history.replaceState(null, "", "/");
          settle({
            status: "signed-out",
            error: error instanceof Error ? error.message : String(error),
          });
        }
        return;
      }

      // Ordinary load: the access token died with the last page, but the
      // refresh cookie may not have.
      const user = await restore();
      settle(user ? { status: "signed-in", user } : { status: "signed-out" });
    };

    void start();

    return () => {
      cancelled = true;
    };
  }, []);

  if (state.status === "loading") {
    return (
      <main>
        <h1>Einkaufsliste</h1>
        <p>Checking your session…</p>
      </main>
    );
  }

  if (state.status === "signed-out") {
    return (
      <main>
        <h1>Einkaufsliste</h1>
        {state.error && <p role="alert">{state.error}</p>}
        <p>
          <button type="button" onClick={() => void beginSignIn()}>
            Sign in with Google
          </button>
        </p>
      </main>
    );
  }

  return (
    <main>
      <h1>Einkaufsliste</h1>
      <p>
        Signed in as <strong>{state.user.display_name}</strong>.
      </p>
      <p>
        There is no list yet — that arrives with M2. This page exists to prove
        sign-in works end to end.
      </p>
      <p>
        <button
          type="button"
          onClick={() => {
            void signOut().then(() => setState({ status: "signed-out" }));
          }}
        >
          Sign out
        </button>
      </p>
    </main>
  );
}
