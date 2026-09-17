import { expect, test } from "@playwright/test";

const session = {
  summary: { session_id: "export-session", session_source: "header.session_id", request_count: 1, providers: [] },
  traces: [], timeline: [], analysis: [],
};

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("llm-tracelab.monitor.language", "en");
    localStorage.setItem("llm-tracelab.monitor.token", "test-monitor-jwt");
  });
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/auth/status") return route.fulfill({ json: { auth_required: false } });
    if (path === "/api/sessions/export-session") return route.fulfill({ json: session });
    return route.fulfill({ json: {} });
  });
});

test("session ATIF export requires a click and downloads authenticated JSONL", async ({ page }) => {
  let calls = 0;
  const record = { schema_version: "ATIF-v1.7", session_id: "export-session", agent: { name: "unknown", version: "unknown" }, steps: [{ step_id: 1, source: "user", message: "hello" }], extra: { warnings: [] } };
  await page.route("**/api/sessions/export-session/trajectory", async (route) => {
    calls++;
    expect(route.request().headers().authorization).toBe("Bearer test-monitor-jwt");
    await route.fulfill({ contentType: "application/x-ndjson", body: JSON.stringify(record) + "\n" });
  });
  await page.goto("/sessions/export-session");
  const button = page.getByRole("button", { name: "Export trajectory (ATIF)" });
  await expect(button).toBeEnabled();
  expect(calls).toBe(0);
  const downloadPromise = page.waitForEvent("download");
  await button.click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("session-export-session.atif.jsonl");
  const stream = await download.createReadStream();
  let body = "";
  for await (const chunk of stream) body += chunk.toString();
  expect(body).toBe(JSON.stringify(record) + "\n");
  await expect(page.getByText("Trajectory downloaded. One complete session per JSONL line.")).toBeVisible();
  expect(calls).toBe(1);
});

test("session ATIF export shows a failure and allows retry", async ({ page }) => {
  await page.route("**/api/sessions/export-session/trajectory", (route) => route.fulfill({ status: 404, json: { error: "Session has no recorded requests" } }));
  await page.goto("/sessions/export-session");
  const button = page.getByRole("button", { name: "Export trajectory (ATIF)" });
  await button.click();
  await expect(page.getByText("Session has no recorded requests")).toBeVisible();
  await expect(button).toBeEnabled();
});
