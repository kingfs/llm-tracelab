export const MONITOR_TOKEN_KEY = "llm-tracelab.monitor.token";

export const apiPaths = {
  authStatus: "/api/auth/status",
  authCheck: "/api/auth/check",
  authLogin: "/api/auth/login",
  authMe: "/api/auth/me",
  authPassword: "/api/auth/password",
  authTokens: "/api/auth/tokens",
  overview: "/api/overview",
  events: "/api/events",
  eventsSummary: "/api/events/summary",
  eventsStream: "/api/events/stream",
  eventsReadAll: "/api/events/read-all",
  eventRead: (eventID) => `/api/events/${encodeURIComponent(eventID)}/read`,
  eventResolve: (eventID) => `/api/events/${encodeURIComponent(eventID)}/resolve`,
  eventIgnore: (eventID) => `/api/events/${encodeURIComponent(eventID)}/ignore`,
  traces: "/api/traces",
  findings: "/api/findings",
  responsesAuditTrace: "/api/responses/audit/trace",
  responsesFunctionExecutors: "/api/responses/function-executors",
  analysis: "/api/analysis",
  analysisJobs: "/api/analysis/jobs",
  analysisBatchReanalyze: "/api/analysis/batch/reanalyze",
  trace: (traceID) => `/api/traces/${encodeURIComponent(traceID)}`,
  traceRaw: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/raw`,
  traceObservation: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/observation`,
  traceFindings: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/findings`,
  tracePerformance: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/performance`,
  traceDownload: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/download`,
  traceRepairUsage: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/repair-usage`,
  traceReanalyze: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/reanalyze`,
  sessions: "/api/sessions",
  session: (sessionID) => `/api/sessions/${encodeURIComponent(sessionID)}`,
  sessionTrajectory: (sessionID) => `/api/sessions/${encodeURIComponent(sessionID)}/trajectory`,
  sessionReanalyze: (sessionID) => `/api/sessions/${encodeURIComponent(sessionID)}/reanalyze`,
  models: "/api/models",
  model: (model) => `/api/models/${encodeURIComponent(model)}`,
  modelSpecLookup: (model) => `/api/models/${encodeURIComponent(model)}/spec-lookup`,
  providers: "/api/channels",
  provider: (providerID) => `/api/channels/${encodeURIComponent(providerID)}`,
  providerProbe: (providerID) => `/api/channels/${encodeURIComponent(providerID)}/probe`,
  providerModels: (providerID) => `/api/channels/${encodeURIComponent(providerID)}/models`,
  providerModelsBatch: (providerID) => `/api/channels/${encodeURIComponent(providerID)}/models/batch`,
  providerModel: (providerID, model) => `/api/channels/${encodeURIComponent(providerID)}/models/${encodeURIComponent(model)}`,
  providerProbePreview: "/api/provider-probe",
  providerProbeReport: "/api/provider-probe/report",
  providerProbeApply: "/api/provider-probe/report/apply",
  providerSetupValidate: "/api/provider-setup/validate",
  providerSetupApply: "/api/provider-setup/apply",
  providerPresets: "/api/provider-presets",
  routingExchanges: "/api/routing/exchanges",
  routingSummary: "/api/routing/summary",
  routingInspect: "/api/routing/inspect",
  routingSettings: "/api/settings/routing",
  modelAliases: "/api/model-aliases",
  modelAliasValidate: "/api/model-aliases/validate",
  modelAlias: (aliasID) => `/api/model-aliases/${encodeURIComponent(aliasID)}`,
  upstream: (upstreamID) => `/api/upstreams/${encodeURIComponent(upstreamID)}`,
};

export function monitorAuthHeaders() {
  const token = window.localStorage.getItem(MONITOR_TOKEN_KEY) || "";
  return token ? { Authorization: `Bearer ${token}` } : {};
}

export function apiURL(path, params = null) {
  const query = params instanceof URLSearchParams ? params.toString() : new URLSearchParams(params || {}).toString();
  return query ? `${path}?${query}` : path;
}

export async function requestJSON(path, { method = "GET", headers = {}, body, signal } = {}) {
  const requestHeaders = {
    ...monitorAuthHeaders(),
    ...headers,
  };
  const response = await fetch(path, {
    method,
    headers: requestHeaders,
    body,
    signal,
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    const err = new Error(payload.error || payload.error_text || `request failed: ${response.status}`);
    err.payload = payload;
    err.status = response.status;
    throw err;
  }
  return payload;
}

export function postJSON(path, payload, options = {}) {
  return requestJSON(path, {
    ...options,
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
    body: JSON.stringify(payload),
  });
}

export function patchJSON(path, payload, options = {}) {
  return requestJSON(path, {
    ...options,
    method: "PATCH",
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
    body: JSON.stringify(payload),
  });
}

export function deleteJSON(path, options = {}) {
  return requestJSON(path, {
    ...options,
    method: "DELETE",
  });
}

export async function downloadBlob(path) {
  const response = await fetch(path, { headers: monitorAuthHeaders() });
  if (!response.ok) {
    const payload = await response.json().catch(() => ({}));
    throw new Error(payload.error || `request failed: ${response.status}`);
  }
  return response.blob();
}

export function listItems(payload) {
  return Array.isArray(payload?.items) ? payload.items : [];
}
