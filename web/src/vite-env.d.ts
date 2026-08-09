/// <reference types="vite/client" />

interface ImportMetaEnv {
  /**
   * The Google OAuth client id. Public by design — it identifies the app, it
   * does not authorise anything. The client *secret* stays on the server.
   */
  readonly VITE_GOOGLE_CLIENT_ID?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
