import { defineConfig, devices } from "@playwright/test";

// Playwright normally resolves its own managed browser. Set
// PLAYWRIGHT_CHROMIUM_EXECUTABLE only to point at a system Chromium instead.
const chromiumPath = (process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || "").trim();
const launchOptions = chromiumPath ? { executablePath: chromiumPath } : {};
const baseURL = process.env.MONITOR_REAL_BASE_URL || "http://127.0.0.1:4183";
const serverURL = new URL(baseURL);
const serverAddr = `${serverURL.hostname}:${serverURL.port || "80"}`;

export default defineConfig({
  testDir: "./tests-real",
  timeout: 45_000,
  expect: { timeout: 8_000 },
  reporter: [["list"]],
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: {
    command: `MONITOR_REAL_ADDR=${serverAddr} go run ./test-fixtures/monitor_real_server.go`,
    url: baseURL,
    reuseExistingServer: false,
    timeout: 120_000,
  },
  projects: [
    {
      name: "desktop-real",
      use: {
        ...devices["Desktop Chrome"],
        channel: undefined,
        launchOptions,
      },
    },
  ],
});
