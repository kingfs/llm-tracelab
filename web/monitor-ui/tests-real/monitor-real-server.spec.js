import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  // The Monitor UI defaults to Chinese when no language is persisted, while
  // this suite asserts English labels. Pin the language for every test.
  await page.addInitScript(() => {
    window.localStorage.setItem("llm-tracelab.monitor.language", "en");
  });
});

test("real monitor server renders seeded model and channel data", async ({ page }) => {
  await page.goto("/models");
  await expect(page.getByRole("heading", { name: "Models", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: /gpt-5/i })).toBeVisible();
  await expect(page.getByText("openai-primary").first()).toBeVisible();

  await page.getByRole("link", { name: /gpt-5/i }).first().click();
  await expect(page.getByRole("heading", { name: "gpt-5" })).toBeVisible();
  await expect(page.getByText("Provider coverage")).toBeVisible();
  await expect(page.getByRole("link", { name: /openai-primary manual enabled/i })).toBeVisible();

  await page.goto("/providers/openai-primary");
  await expect(page.getByRole("heading", { name: "OpenAI Primary" })).toBeVisible();
  await expect(page.getByText("encrypted-local").first()).toBeVisible();
  await expect(page.getByText("rate limited")).toBeVisible();

  await page.getByRole("button", { name: "Edit provider" }).click();
  await expect(page.getByRole("heading", { name: "Edit provider" })).toBeVisible();
  await expect(page.getByLabel("Provider preset")).toHaveValue("openai");
  await page.getByRole("button", { name: "Advanced options" }).click();
  await expect(page.locator("textarea")).toContainText("Authorization: ***");
  await expect(page.locator("textarea")).toContainText("X-Test: visible");
});

test("real monitor server supports probe and manual model mutation", async ({ page }) => {
  await page.goto("/providers/openai-primary");

  await page.getByPlaceholder("Add model manually").fill("gpt-manual-real");
  await page.getByRole("button", { name: "Add model" }).click();
  await expect(page.getByText("gpt-manual-real")).toBeVisible();

  await page.getByRole("button", { name: "Probe provider" }).click();
  await expect(page.getByText("success").first()).toBeVisible();
  await expect(page.getByText("discovered, awaiting enable").first()).toBeVisible();
  await page.getByRole("button", { name: "Enable new (1)" }).click();
  await expect(page.getByText("gpt-new-real")).toBeVisible();
});

test("real monitor server shows probe failure guidance", async ({ page }) => {
  await page.goto("/providers/broken-auth");
  await expect(page.getByRole("heading", { name: "Broken Auth" })).toBeVisible();

  await page.getByRole("button", { name: "Probe provider" }).click();
  await expect(page.getByText("not_found").first()).toBeVisible();
  await expect(page.getByText(/Check the base URL/i).first()).toBeVisible();
});

test("real monitor server renders routing decision records", async ({ page }) => {
  await page.goto("/routing");
  await expect(page.getByRole("heading", { name: "Routing" })).toBeVisible();
  await expect(page.getByText("Recent selected routes")).toBeVisible();
  await expect(page.getByText("Routed requests")).toBeVisible();
  await expect(page.getByText("gpt-5").first()).toBeVisible();
  await expect(page.getByRole("link", { name: "View trace" }).first()).toBeVisible();
});

test("real monitor server serves trace routing links", async ({ page }) => {
  const fixture = await page.request.get("/__fixture/state").then((response) => response.json());
  await page.goto(`/traces/${fixture.routed_trace_id}`);
  await expect(page.getByRole("heading", { name: "Selected route target" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Open Channel" })).toHaveAttribute("href", "/providers/openai-primary");
  await expect(page.getByRole("link", { name: "Open Upstream" })).toHaveAttribute("href", "/upstreams/openai-primary");
});
