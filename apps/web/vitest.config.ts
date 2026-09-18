import { defineConfig } from "vitest/config";

// Node environment, not jsdom: every unit test this project has so far
// (starting with sseReconnect.test.ts) exercises plain TypeScript logic
// against injected fakes, never the real DOM — no need to pay jsdom's
// weight until a test actually needs it.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
