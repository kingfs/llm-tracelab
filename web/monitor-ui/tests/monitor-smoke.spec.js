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
    if (path === "/api/secrets/local-key" && method === "GET" && !url.searchParams.has("export")) {
      return route.fulfill({ json: localSecretPayload() });
    }
    if (path === "/api/secrets/local-key" && method === "GET" && url.searchParams.get("export") === "1") {
      return route.fulfill({ body: "mock-secret-key\n", headers: { "Content-Type": "text/plain" } });
    }
    if (path === "/api/secrets/local-key" && method === "POST" && url.searchParams.get("rotate") === "1") {
      return route.fulfill({ json: { old_fingerprint: "abc123", new_fingerprint: "def456", channel_count: 1, api_key_count: 1, header_count: 1 } });
    }
    if (path === "/api/channels/openai-primary" && method === "GET") {
      return route.fulfill({ json: channelDetailPayload() });
    }
    if (path === "/api/channels/openai-primary" && method === "PATCH") {
      return route.fulfill({ json: channelDetailPayload() });
    }
    if (path === "/api/channels/openai-primary/probe") {
      const body = route.request().postDataJSON();
      expect(body.enable_discovered).toBe(false);
      return route.fulfill({ status: 502, json: probeFailurePayload() });
    }
    if (path === "/api/channels/openai-primary/models") {
      return route.fulfill({ json: { model: "gpt-manual", enabled: true, source: "manual" } });
    }
    if (path === "/api/channels/openai-primary/models/gpt-5") {
      return route.fulfill({ json: { ok: true } });
    }
    if (path === "/api/channels/openai-primary/models/batch" && method === "PATCH") {
      const body = route.request().postDataJSON();
      expect(body).toEqual({ models: ["gpt-new"], enabled: true });
      return route.fulfill({ json: { updated: 1, models: ["gpt-new"], enabled: true } });
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
  await expect(page.getByText("encrypted-local").first()).toBeVisible();
  await expect(page.getByRole("heading", { name: "Channel secret storage" })).toBeVisible();
  await expect(page.getByText("abc123")).toBeVisible();
  await expect(page.getByRole("button", { name: "Rotate key" })).toBeDisabled();

  await page.getByRole("link", { name: /OpenAI Primary/i }).first().click();
  await expect(page.getByRole("heading", { name: "OpenAI Primary" })).toBeVisible();
  await expect(page.getByText("encrypted-local").first()).toBeVisible();
  await expect(page.getByText("discovered, awaiting enable")).toBeVisible();

  await page.getByRole("button", { name: "Edit" }).click();
  await expect(page.getByRole("heading", { name: "Edit channel" })).toBeVisible();
  await expect(page.locator("textarea")).toContainText("Authorization: ***");

  await page.getByPlaceholder("Add model manually").fill("gpt-manual");
  await page.getByRole("button", { name: "Add model" }).click();
  await expect(page.getByRole("button", { name: "Adding" })).toBeHidden();

  await page.getByRole("button", { name: "Probe" }).click();
  await expect(page.getByRole("button", { name: "Probing" })).toBeHidden();
  await expect(page.getByText("auth_error").first()).toBeVisible();
  await expect(page.getByText(/Verify the API key/i).first()).toBeVisible();

  await page.getByRole("button", { name: "Enable new (1)" }).click();
  await expect(page.getByRole("button", { name: "Enabling" })).toBeHidden();
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
      { channel_id: "openai-primary", model: "gpt-new", enabled: false, source: "discovered", summary: usageSummary() },
    ],
    recent_failures: [{
      trace_id: "trace-failed",
      model: "gpt-5",
      status_code: 429,
      reason: "http_429",
      recorded_at: new Date().toISOString(),
      error_text: "rate limited",
    }],
    recent_probe_runs: [{
      id: "probe-success",
      status: "success",
      started_at: new Date().toISOString(),
      completed_at: new Date().toISOString(),
      duration_ms: 10,
      discovered_count: 2,
      enabled_count: 2,
      endpoint: "/v1/models",
    }, {
      id: "probe-failed",
      status: "failed",
      failure_reason: "auth_error",
      retry_hint: "Verify the API key and custom authorization headers for this channel.",
      started_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      completed_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      duration_ms: 12,
      discovered_count: 0,
      enabled_count: 0,
      endpoint: "/v1/models",
      error_text: "upstream status: 401 Unauthorized",
    }],
  };
}

function localSecretPayload() {
  return {
    mode: "encrypted-local",
    key_path: "/tmp/traces/trace_index.secret",
    exists: true,
    readable: true,
    fingerprint: "abc123",
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

function probeFailurePayload() {
  return {
    channel_id: "openai-primary",
    status: "failed",
    failure_reason: "auth_error",
    retry_hint: "Verify the API key and custom authorization headers for this channel.",
    models: [],
    discovered_count: 0,
    enabled_count: 0,
    endpoint: "/v1/models",
    error_text: "upstream status: 401 Unauthorized",
    started_at: new Date().toISOString(),
    completed_at: new Date().toISOString(),
    duration_ms: 12,
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
