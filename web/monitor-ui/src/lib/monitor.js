export function formatEndpointTag(value = "") {
  const endpoint = String(value || "").toLowerCase();
  if (endpoint.includes("/v1/chat/completions")) {
    return "chat";
  }
  if (endpoint.includes("/v1/responses")) {
    return "resp";
  }
  if (endpoint.includes("/v1/messages")) {
    return "msg";
  }
  if (endpoint.includes("/v1/models")) {
    return "models";
  }
  return value || "api";
}

export function formatProviderTag(value = "") {
  if (!value) {
    return "unknown";
  }
  if (value === "openai_compatible") {
    return "openai";
  }
  return String(value).replaceAll("_", " ");
}

export function formatHealthLabel(value = "") {
  if (!value) {
    return "unknown";
  }
  return String(value).replaceAll("_", " ");
}

export function healthTone(value = "") {
  switch (value) {
    case "healthy":
      return "green";
    case "degraded":
    case "probation":
      return "gold";
    case "open":
      return "danger";
    default:
      return "default";
  }
}

export function formatRatio(value) {
  const number = Number(value || 0);
  return `${(number * 100).toFixed(1)}%`;
}

export function formatCapacity(weight, capacityHint) {
  return `${Number(weight || 0).toFixed(1)} x ${Number(capacityHint || 0).toFixed(1)}`;
}

export function formatRoutingScore(value = 0) {
  return Number(value || 0).toFixed(2);
}

export function formatMultiplier(value = 0) {
  const number = Number(value || 0);
  if (number <= 0) {
    return "-";
  }
  return `${number.toFixed(2)}x`;
}

export function formatFailureReason(value = "") {
  if (!value) {
    return "-";
  }
  return String(value).replaceAll("_", " ");
}

export function buildRoutingDecisionSummary({ upstreamID = "", policy = "", score = 0, candidateCount = 0, failureReason = "" }) {
  const target = upstreamID || "unknown upstream";
  const policyLabel = policy || "selected";
  if (!upstreamID && failureReason) {
    return `The router did not pick an upstream because it failed with ${formatFailureReason(failureReason)}.`;
  }
  if (candidateCount > 0) {
    return `${target} was chosen by ${policyLabel} from ${candidateCount} candidate${candidateCount > 1 ? "s" : ""} with score ${formatRoutingScore(score)}.`;
  }
  return `${target} was chosen by ${policyLabel} with score ${formatRoutingScore(score)}.`;
}

export function summarizeTraceFailure(detail) {
  if (!detail?.header?.meta) {
    return null;
  }
  const header = detail.header.meta;
  const statusCode = Number(header.status_code || 0);
  const blocks = detail.ai_blocks || [];
  const providerError = blocks.find((block) => block.title === "Provider Error");
  if (providerError) {
    return {
      title: "Provider Error",
      summary: `The upstream provider returned an error response for this request.`,
      detail: providerError.text || providerError.meta || "",
    };
  }
  const refusal = blocks.find((block) => block.title === "Refusal");
  if (refusal) {
    return {
      title: "Refusal",
      summary: `The model refused to continue this request.`,
      detail: refusal.text || refusal.meta || "",
    };
  }
  if (header.error) {
    return {
      title: "Trace Error",
      summary: `The proxy recorded an error while handling this request.`,
      detail: header.error,
    };
  }
  if (statusCode >= 400) {
    return {
      title: "HTTP Failure",
      summary: `This request completed with HTTP ${statusCode}.`,
      detail: "",
    };
  }
  return null;
}

export function summarizeSessionItems(items = []) {
  const summary = items.reduce(
    (state, item) => {
      state.totalSessions += 1;
      state.totalRequests += Number(item.request_count || 0);
      state.totalTokens += Number(item.total_tokens || 0);
      state.successRateSum += Number(item.success_rate || 0);
      return state;
    },
    { totalSessions: 0, totalRequests: 0, totalTokens: 0, successRateSum: 0 }
  );
  return {
    totalSessions: summary.totalSessions,
    totalRequests: summary.totalRequests,
    totalTokens: summary.totalTokens,
    avgSuccessRate: summary.totalSessions ? summary.successRateSum / summary.totalSessions : 0,
  };
}

export function buildTraceLink(traceID, fromView = "", fromSessionID = "", tab = "", focus = "") {
  const params = new URLSearchParams();
  const normalizedTab = normalizeTraceTab(tab);
  if (fromView) {
    params.set("view", fromView);
  }
  if (fromSessionID) {
    params.set("from_session", fromSessionID);
  }
  if (tab && normalizedTab !== "conversation") {
    params.set("tab", normalizedTab);
  }
  if (focus) {
    params.set("focus", focus);
  }
  const query = params.toString();
  return query ? `/traces/${traceID}?${query}` : `/traces/${traceID}`;
}

export function buildUpstreamLink(upstreamID, windowValue = "24h", modelValue = "") {
  const params = new URLSearchParams();
  if (windowValue && windowValue !== "24h") {
    params.set("window", windowValue);
  }
  if (modelValue) {
    params.set("model", modelValue);
  }
  const query = params.toString();
  return query ? `/upstreams/${encodeURIComponent(upstreamID)}?${query}` : `/upstreams/${encodeURIComponent(upstreamID)}`;
}

export function buildModelLink(model, windowValue = "24h") {
  const params = new URLSearchParams();
  if (windowValue && windowValue !== "24h") {
    params.set("window", windowValue);
  }
  const query = params.toString();
  return query ? `/models/${encodeURIComponent(model)}?${query}` : `/models/${encodeURIComponent(model)}`;
}

export function buildChannelLink(channelID, windowValue = "24h") {
  const params = new URLSearchParams();
  if (windowValue && windowValue !== "24h") {
    params.set("window", windowValue);
  }
  const query = params.toString();
  return query ? `/channels/${encodeURIComponent(channelID)}?${query}` : `/channels/${encodeURIComponent(channelID)}`;
}

export function buildRoutingLink(upstreamWindow = "24h", upstreamModel = "") {
  const params = new URLSearchParams();
  if (upstreamWindow && upstreamWindow !== "24h") {
    params.set("window", upstreamWindow);
  }
  if (upstreamModel) {
    params.set("model", upstreamModel);
  }
  const query = params.toString();
  return query ? `/routing?${query}` : "/routing";
}

export function normalizeAnalyticsWindow(value = "") {
  switch (value) {
    case "1h":
    case "7d":
    case "30d":
    case "all":
      return value;
    default:
      return "24h";
  }
}

export function normalizeTraceTab(value = "") {
  switch (value) {
    case "conversation":
    case "protocol":
    case "audit":
    case "performance":
    case "raw":
      return value;
    case "timeline":
    case "summary":
    case "tools":
      return "conversation";
    default:
      return "conversation";
  }
}

export function setOrDeleteParam(params, key, value) {
  if (value && String(value).trim()) {
    params.set(key, String(value).trim());
    return;
  }
  params.delete(key);
}

export function normalizeUpstreamWindow(value = "") {
  switch (value) {
    case "1h":
    case "7d":
    case "all":
      return value;
    default:
      return "24h";
  }
}

export function buildFailureContexts(timeline = []) {
  const failures = [];
  for (let index = 0; index < timeline.length; index += 1) {
    const current = timeline[index];
    if (current.status_code >= 200 && current.status_code < 300) {
      continue;
    }
    failures.push({
      previous: index > 0 ? timeline[index - 1] : null,
      current,
      next: index < timeline.length - 1 ? timeline[index + 1] : null,
    });
  }
  return failures;
}

export function formatDuration(value, { precise = false } = {}) {
  const ms = Number(value || 0);
  const safeMs = Number.isFinite(ms) ? Math.max(0, Math.round(ms)) : 0;
  let label = `${safeMs} ms`;
  if (safeMs >= 1000) {
    const seconds = safeMs / 1000;
    label = `${formatCompactNumber(seconds)}s`;
  }
  if (precise && label !== `${safeMs} ms`) {
    return `${label} (${safeMs} ms)`;
  }
  return label;
}

export function formatTokenRate(tokens = 0, durationMs = 0) {
  const tokenCount = Number(tokens || 0);
  const ms = Number(durationMs || 0);
  if (!Number.isFinite(tokenCount) || !Number.isFinite(ms) || tokenCount <= 0 || ms <= 0) {
    return "-";
  }
  return `${formatCompactNumber(tokenCount / (ms / 1000))} tok/s`;
}

export function formatPrefillSpeed(promptTokens = 0, ttftMs = 0, durationMs = 0, isStream = false) {
  // stream: prefill ≈ ttft; non-stream: ttft == total, use total as denominator
  const ms = isStream ? Number(ttftMs || 0) : Number(durationMs || 0);
  const tokens = Number(promptTokens || 0);
  if (!Number.isFinite(tokens) || !Number.isFinite(ms) || tokens <= 0 || ms <= 0) {
    return "-";
  }
  return `${formatCompactNumber(tokens / (ms / 1000))} tok/s`;
}

export function formatGenerationSpeed(completionTokens = 0, durationMs = 0, ttftMs = 0, isStream = false) {
  const totalMs = Number(durationMs || 0);
  const tokens = Number(completionTokens || 0);
  // non-stream: all tokens arrive at once, denominator = total
  // stream: tokens generated after ttft, denominator = total - ttft
  const genMs = isStream ? totalMs - Number(ttftMs || 0) : totalMs;
  if (!Number.isFinite(tokens) || !Number.isFinite(genMs) || tokens <= 0 || genMs < 10) {
    return "-";
  }
  return `${formatCompactNumber(tokens / (genMs / 1000))} tok/s`;
}

export function formatCacheRate(cachedTokens = 0, totalTokens = 0) {
  const cached = Number(cachedTokens || 0);
  const total = Number(totalTokens || 0);
  if (!Number.isFinite(cached) || !Number.isFinite(total) || total <= 0) {
    return "-";
  }
  const rate = (cached / total) * 100;
  if (rate < 0.1 && cached > 0) {
    return "<0.1%";
  }
  return `${rate.toFixed(1)}%`;
}

function formatCompactNumber(value = 0) {
  const number = Number(value || 0);
  if (!Number.isFinite(number)) {
    return "0";
  }
  if (number >= 100 || Number.isInteger(number)) {
    return String(Math.round(number));
  }
  if (number >= 10) {
    return number.toFixed(1).replace(/\.0$/, "");
  }
  return number.toFixed(2).replace(/0+$/, "").replace(/\.$/, "");
}

export function formatCount(value = 0) {
  const number = Number(value || 0);
  if (!Number.isFinite(number)) {
    return "0";
  }
  if (Math.abs(number) >= 1_000_000_000) {
    return `${(number / 1_000_000_000).toFixed(1).replace(/\.0$/, "")}B`;
  }
  if (Math.abs(number) >= 1_000_000) {
    return `${(number / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
  }
  if (Math.abs(number) >= 1_000) {
    return `${(number / 1_000).toFixed(1).replace(/\.0$/, "")}K`;
  }
  return String(Math.round(number));
}

export function buildFailureSummary(context) {
  const endpoint = formatEndpointTag(context.current.endpoint);
  const provider = formatProviderTag(context.current.provider);
  const status = context.current.status_code || 0;
  if (context.previous) {
    const durationDelta = formatSignedMetric(context.current.duration_ms - context.previous.duration_ms);
    return `${provider} ${endpoint} failed with HTTP ${status}; duration ${durationDelta} ms vs previous request.`;
  }
  return `${provider} ${endpoint} failed with HTTP ${status}.`;
}

export function buildFailureDelta(previous, current) {
  if (!previous || !current) {
    return null;
  }
  return {
    duration_ms: current.duration_ms - previous.duration_ms,
    total_tokens: current.total_tokens - previous.total_tokens,
  };
}

export function buildFailureDetail(item) {
  const parts = [];
  if (item.endpoint) {
    parts.push(item.endpoint);
  }
  if (item.provider) {
    parts.push(formatProviderTag(item.provider));
  }
  if (item.ttft_ms) {
    parts.push(`ttft ${formatDuration(item.ttft_ms)}`);
  }
  return parts.join(" · ");
}

export function buildUpstreamHealthSummary(target, failureReasons = [], thresholds = null) {
  const health = formatHealthLabel(target?.health_state || "unknown");
  const errorRate = formatRatio(target?.error_rate);
  const timeoutRate = formatRatio(target?.timeout_rate);
  const topReason = failureReasons[0]?.label ? formatFailureReason(failureReasons[0].label) : "no dominant failure reason";
  const signals = [];
  const errorState = resolveThresholdState(target?.error_rate, thresholds?.error_rate_degraded, thresholds?.error_rate_open);
  if (errorState !== "healthy" && errorState !== "unknown") {
    signals.push(`error ${errorState}`);
  }
  const timeoutState = resolveThresholdState(target?.timeout_rate, thresholds?.timeout_rate_degraded, thresholds?.timeout_rate_open);
  if (timeoutState !== "healthy" && timeoutState !== "unknown") {
    signals.push(`timeout ${timeoutState}`);
  }
  const ttftState = resolveThresholdState(computeTTFTRatio(target), thresholds?.ttft_degraded_ratio, null);
  if (ttftState !== "healthy" && ttftState !== "unknown") {
    signals.push(`ttft ${ttftState}`);
  }
  const signalText = signals.length ? ` Thresholds: ${signals.join(", ")}.` : "";
  return `${health} with error ${errorRate}, timeout ${timeoutRate}, dominant failure ${topReason}.${signalText}`;
}

export function buildTraceUpstreamHealthSummary(health) {
  if (!health) {
    return "No upstream health context available.";
  }
  const thresholds = health.health_thresholds || {};
  const errorState = resolveThresholdState(health.error_rate, thresholds.error_rate_degraded, thresholds.error_rate_open);
  const timeoutState = resolveThresholdState(health.timeout_rate, thresholds.timeout_rate_degraded, thresholds.timeout_rate_open);
  const ttftState = resolveThresholdState(computeTTFTRatio(health), thresholds.ttft_degraded_ratio, null);
  return `${formatHealthLabel(health.health_state)} now; error ${formatRatio(health.error_rate)} (${errorState}), timeout ${formatRatio(health.timeout_rate)} (${timeoutState}), ttft ratio ${formatMultiplier(computeTTFTRatio(health))} (${ttftState}).`;
}

export function computeTTFTRatio(target) {
  const fast = Number(target?.ttft_fast_ms || 0);
  const slow = Number(target?.ttft_slow_ms || 0);
  if (fast <= 0 || slow <= 0) {
    return 0;
  }
  return fast / slow;
}

export function resolveThresholdState(value, degradedThreshold, openThreshold) {
  const current = Number(value || 0);
  const degraded = Number(degradedThreshold || 0);
  const open = openThreshold == null ? 0 : Number(openThreshold || 0);
  if (current <= 0 || degraded <= 0) {
    return "unknown";
  }
  if (open > 0 && current >= open) {
    return "open";
  }
  if (current >= degraded) {
    return "degraded";
  }
  return "healthy";
}

export function metricThresholdTone(value = "") {
  switch (value) {
    case "open":
      return "danger";
    case "degraded":
      return "gold";
    case "healthy":
      return "green";
    default:
      return "default";
  }
}

export function formatSignedMetric(value = 0) {
  const number = Number(value || 0);
  if (number > 0) {
    return `+${number}`;
  }
  return String(number);
}

export function formatDateTime(value) {
  if (!value) {
    return "-";
  }
  return new Date(value).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function formatTime(value) {
  if (!value) {
    return "-";
  }
  return new Date(value).toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function formatTimelineBucketLabel(value) {
  if (!value) {
    return "-";
  }
  return new Date(value).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}
