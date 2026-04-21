import { startTransition, useEffect, useState } from "react";

export function useJSON(url, deps = []) {
  const [state, setState] = useState({ loading: true, data: null, error: "" });

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();

    startTransition(() => {
      setState((current) => ({ ...current, loading: true, error: "" }));
    });

    fetch(url, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) {
          const payload = await response.json().catch(() => ({}));
          throw new Error(payload.error || `request failed: ${response.status}`);
        }
        return response.json();
      })
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
  }, [url, ...deps]);

  return state;
}
