export const MONITOR_TOKEN_KEY = "llm-tracelab.monitor.token";

export const apiPaths = {
  authStatus: "/api/auth/status",
  authCheck: "/api/auth/check",
  authLogin: "/api/auth/login",
  authTokens: "/api/auth/tokens",
  traces: "/api/traces",
  findings: "/api/findings",
  analysis: "/api/analysis",
  trace: (traceID) => `/api/traces/${encodeURIComponent(traceID)}`,
  traceRaw: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/raw`,
  traceObservation: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/observation`,
  traceFindings: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/findings`,
  tracePerformance: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/performance`,
  traceDownload: (traceID) => `/api/traces/${encodeURIComponent(traceID)}/download`,
  sessions: "/api/sessions",
  session: (sessionID) => `/api/sessions/${encodeURIComponent(sessionID)}`,
  sessionAnalysis: (sessionID) => `/api/sessions/${encodeURIComponent(sessionID)}/analysis`,
  upstreams: "/api/upstreams",
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
    throw new Error(payload.error || `request failed: ${response.status}`);
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

export function listTotal(payload) {
  return Number(payload?.total || payload?.total_count || listItems(payload).length || 0);
}
