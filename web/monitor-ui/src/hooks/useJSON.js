import { startTransition, useEffect, useState } from "react";
import { monitorAuthHeaders, MONITOR_TOKEN_KEY, requestJSON } from "../lib/api";

export { monitorAuthHeaders, MONITOR_TOKEN_KEY };

export function useJSON(url, deps = []) {
  const [state, setState] = useState({ loading: true, data: null, error: "" });
  const requestKey = JSON.stringify([url, ...deps]);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    const requestURL = url;

    startTransition(() => {
      setState((current) => ({ ...current, loading: true, error: "" }));
    });

    requestJSON(requestURL, { signal: controller.signal })
      .then((data) => {
        if (cancelled) {
          return;
        }
        startTransition(() => {
          setState({ loading: false, data, error: "" });
        });
      })
      .catch((error) => {
        if (cancelled || error.name === "AbortError") {
          return;
        }
        startTransition(() => {
          setState({ loading: false, data: null, error: error.message || "unknown error" });
        });
      });

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [requestKey, url]);

  return state;
}
