import { useEffect, useState } from "react";

import type { components } from "./api/schema";

// Straight from api/openapi.yaml (non-negotiable 4). Editing the contract
// without regenerating breaks the build here, not silently at runtime.
type Health = components["schemas"]["Health"];

type Reachability =
  | { state: "checking" }
  | { state: "reachable"; status: Health["status"] }
  | { state: "unreachable"; detail: string };

// Same-origin, so no base URL: Caddy serves this app and proxies the API on
// one origin in production, and Vite proxies the same paths in dev (§5.1).
async function fetchHealth(signal: AbortSignal): Promise<Health> {
  const response = await fetch("/healthz", { signal });
  if (!response.ok) {
    throw new Error(`/healthz returned ${response.status}`);
  }
  return (await response.json()) as Health;
}

export function App() {
  const [api, setApi] = useState<Reachability>({ state: "checking" });

  useEffect(() => {
    const controller = new AbortController();

    fetchHealth(controller.signal)
      .then((health) => setApi({ state: "reachable", status: health.status }))
      .catch((error: unknown) => {
        if (controller.signal.aborted) {
          return;
        }
        setApi({
          state: "unreachable",
          detail: error instanceof Error ? error.message : String(error),
        });
      });

    return () => controller.abort();
  }, []);

  return (
    <main>
      <h1>Einkaufsliste</h1>
      <p>
        Nothing to show yet — there is no list, and no way to sign in. This
        page exists to prove the app builds, ships and reaches its API.
      </p>
      <p data-testid="api-status">
        API: {api.state === "checking" && "checking…"}
        {api.state === "reachable" && api.status}
        {api.state === "unreachable" && `unreachable (${api.detail})`}
      </p>
    </main>
  );
}
