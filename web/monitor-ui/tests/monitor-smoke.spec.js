import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();

    if (path === "/api/auth/status") {
      return route.fulfill({ json: { auth_required: false } });
    }
    if (path === "/api/models") {
      return route.fulfill({ json: modelListPayload() });
    }
    if (path === "/api/models/gpt-5") {
      return route.fulfill({ json: modelDetailPayload() });
    }
    if (path === "/api/channels") {
      return route.fulfill({ json: channelListPayload() });
    }
    if (path === "/api/channels/openai-primary" && method === "GET") {
      return route.fulfill({ json: channelDetailPayload() });
    }
    if (path === "/api/channels/openai-primary" && method === "PATCH") {
      return route.fulfill({ json: channelDetailPayload() });
    }
    if (path === "/api/channels/openai-primary/probe") {
      return route.fulfill({ json: probePayload() });
    }
    if (path === "/api/channels/openai-primary/models") {
      return route.fulfill({ json: { model: "gpt-manual", enabled: true, source: "manual" } });
    }
    if (path === "/api/channels/openai-primary/models/gpt-5") {
      return route.fulfill({ json: { ok: true } });
    }
    if (path === "/api/traces/trace-routed") {
      return route.fulfill({ json: tracePayload() });
    }
    if (path === "/api/traces/trace-routed/raw") {
      return route.fulfill({ json: { data: { request_protocol: "{}", response_protocol: "{}" } } });
    }
    if (path === "/api/traces/trace-routed/observation" || path === "/api/traces/trace-routed/findings" || path === "/api/traces/trace-routed/performance") {
      return route.fulfill({ json: {} });
    }
    return route.fulfill({ status: 404, json: { error: `unhandled ${method} ${path}` } });
  });
});

test("models marketplace and detail render", async ({ page }) => {
  await page.goto("/models");
  await expect(page.getByRole("heading", { name: "Models", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: /gpt-5/i })).toBeVisible();

  await page.getByRole("link", { name: /gpt-5/i }).first().click();
  await expect(page.getByRole("heading", { name: "gpt-5" })).toBeVisible();
  await expect(page.getByText("Provider coverage")).toBeVisible();
  await expect(page.getByText("openai-primary")).toBeVisible();
});

test("channel management renders and supports core actions", async ({ page }) => {
  await page.goto("/channels");
  await expect(page.getByRole("heading", { name: "Channels", exact: true })).toBeVisible();
  await expect(page.getByText("encrypted-local")).toBeVisible();

  await page.getByRole("link", { name: /OpenAI Primary/i }).first().click();
  await expect(page.getByRole("heading", { name: "OpenAI Primary" })).toBeVisible();
  await expect(page.getByText("encrypted-local")).toBeVisible();

  await page.getByRole("button", { name: "Edit" }).click();
  await expect(page.getByRole("heading", { name: "Edit channel" })).toBeVisible();
  await expect(page.locator("textarea")).toContainText("Authorization: ***");

  await page.getByPlaceholder("Add model manually").fill("gpt-manual");
  await page.getByRole("button", { name: "Add model" }).click();
  await expect(page.getByRole("button", { name: "Adding" })).toBeHidden();

  await page.getByRole("button", { name: "Probe" }).click();
  await expect(page.getByRole("button", { name: "Probing" })).toBeHidden();
});

test("trace routing links to channel and upstream views", async ({ page }) => {
  await page.goto("/traces/trace-routed");
  await expect(page.getByText("Routing decision")).toBeVisible();
  await expect(page.getByRole("link", { name: "Open Channel" })).toHaveAttribute("href", "/channels/openai-primary");
  await expect(page.getByRole("link", { name: "Open Upstream" })).toHaveAttribute("href", "/upstreams/openai-primary");
});

function modelListPayload() {
  return {
    refreshed_at: new Date().toISOString(),
    window: "24h",
    items: [{
      model: "gpt-5",
      display_name: "gpt-5",
      provider_count: 1,
      channel_count: 1,
      enabled_channel_count: 1,
      channels: ["openai-primary"],
      summary: usageSummary({ request_count: 12, total_tokens: 23000 }),
      today: usageSummary({ request_count: 4, total_tokens: 7000 }),
    }],
  };
}

function modelDetailPayload() {
  return {
    model: modelListPayload().items[0],
    trends: trendItems(),
    channels: [{
      channel_id: "openai-primary",
      model: "gpt-5",
      enabled: true,
      source: "manual",
      summary: usageSummary({ request_count: 12, total_tokens: 23000 }),
    }],
    refreshed_at: new Date().toISOString(),
    window: "24h",
  };
}

function channelListPayload() {
  return {
    refreshed_at: new Date().toISOString(),
    items: [channelDetailPayload()],
  };
}

function channelDetailPayload() {
  return {
    id: "openai-primary",
    name: "OpenAI Primary",
    base_url: "https://api.openai.example/v1",
    provider_preset: "openai",
    api_key_hint: "sk-...test",
    secret_storage_mode: "encrypted-local",
    headers: { Authorization: "***", "X-Test": "visible" },
    enabled: true,
    priority: 100,
    weight: 1,
    capacity_hint: 1,
    model_discovery: "list_models",
    allow_unknown_models: false,
    model_count: 2,
    enabled_model_count: 2,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    last_probe_status: "success",
    summary: usageSummary({ request_count: 12, failed_request: 1, total_tokens: 23000 }),
    trends: trendItems(),
    models_usage: [
      { channel_id: "openai-primary", model: "gpt-5", enabled: true, source: "manual", summary: usageSummary({ request_count: 10, total_tokens: 20000 }) },
      { channel_id: "openai-primary", model: "gpt-4.1", enabled: true, source: "manual", summary: usageSummary({ request_count: 2, total_tokens: 3000 }) },
    ],
    recent_failures: [{
      trace_id: "trace-failed",
      model: "gpt-5",
      status_code: 429,
      reason: "http_429",
      recorded_at: new Date().toISOString(),
      error_text: "rate limited",
    }],
  };
}

function tracePayload() {
  return {
    header: {
      meta: {
        request_id: "trace-routed",
        time: new Date().toISOString(),
        model: "gpt-5",
        provider: "openai",
        endpoint: "/v1/responses",
        status_code: 200,
        duration_ms: 1200,
        ttft_ms: 120,
        selected_upstream_id: "openai-primary",
        selected_upstream_base_url: "https://api.openai.example/v1",
        selected_upstream_provider_preset: "openai",
        routing_policy: "p2c",
        routing_score: 0.82,
        routing_candidate_count: 2,
      },
      usage: { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120 },
      layout: { is_stream: false },
    },
    messages: [{ role: "user", content: "hello", message_type: "message" }],
    events: [],
    tools: [],
  };
}

function probePayload() {
  return {
    channel_id: "openai-primary",
    status: "success",
    models: ["gpt-5"],
    discovered_count: 1,
    enabled_count: 1,
    started_at: new Date().toISOString(),
    completed_at: new Date().toISOString(),
    duration_ms: 10,
  };
}

function trendItems() {
  const now = Date.now();
  return Array.from({ length: 6 }, (_, index) => ({
    time: new Date(now - (5 - index) * 60 * 60 * 1000).toISOString(),
    request_count: index + 1,
    failed_request: index === 4 ? 1 : 0,
    total_tokens: (index + 1) * 1000,
    model_count: 1,
  }));
}

function usageSummary(overrides = {}) {
  return {
    request_count: 0,
    success_request: 0,
    failed_request: 0,
    success_rate: 100,
    total_tokens: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    cached_tokens: 0,
    avg_ttft: 0,
    avg_duration_ms: 0,
    last_seen: new Date().toISOString(),
    ...overrides,
  };
}
