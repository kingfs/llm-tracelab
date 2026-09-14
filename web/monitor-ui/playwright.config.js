import { defineConfig, devices } from "@playwright/test";

// Playwright normally resolves its own managed browser. Set
// PLAYWRIGHT_CHROMIUM_EXECUTABLE only to point at a system Chromium instead.
const chromiumPath = (process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || "").trim();
const launchOptions = chromiumPath ? { executablePath: chromiumPath } : {};

export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  expect: { timeout: 5_000 },
  reporter: [["list"]],
  use: {
    baseURL: "http://127.0.0.1:4173",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: {
    command: "bun run build && bunx vite preview --host 127.0.0.1 --port 4173",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  projects: [
    {
      name: "desktop",
      use: {
        ...devices["Desktop Chrome"],
        channel: undefined,
        launchOptions,
      },
    },
    {
      name: "mobile",
      use: {
        ...devices["Pixel 7"],
        channel: undefined,
        launchOptions,
      },
    },
  ],
});
