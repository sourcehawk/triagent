import { defineConfig, devices } from "@playwright/test";

// The Go harness boots the launcher on a random loopback port and passes
// the base URL + launch token to Playwright via env (TRIAGENT_BASE_URL /
// TRIAGENT_TOKEN). There is no webServer block — the harness owns the
// launcher lifecycle; Playwright only drives the browser against an
// already-running instance.
//
// Behavioural assertions only (selector exists, text contains, click
// triggers state) — no screenshot diffing. Chromium-only keeps the CI
// browser cache small; the suite asserts product behaviour, not
// cross-engine rendering.
const baseURL = process.env.TRIAGENT_BASE_URL ?? "http://127.0.0.1:8080";

export default defineConfig({
  testDir: "./specs",
  // The harness runs one spec per (*Harness).Browser.Run call against its
  // own launcher, so parallelism across files would cross-wire launchers.
  // Within a single spec file, serial keeps the shared launcher state
  // (one investigation) deterministic.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
