import { defineConfig } from "vitest/config";
import path from "node:path";

// Vitest config for the frontend unit tests.
// lib/ tests run in node (no DOM); components/ and hooks/ tests run in
// jsdom and extend jest-dom matchers via vitest.setup.ts. Hook tests
// need jsdom because @testing-library/react's renderHook touches the
// DOM.
export default defineConfig({
  test: {
    projects: [
      {
        extends: true,
        test: {
          name: "node",
          include: ["lib/**/*.test.ts"],
          environment: "node",
        },
      },
      {
        extends: true,
        test: {
          name: "jsdom",
          include: ["components/**/*.test.tsx", "hooks/**/*.test.ts"],
          environment: "jsdom",
        },
      },
    ],
    setupFiles: ["./vitest.setup.ts"],
    globals: true,
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "."),
    },
  },
});
