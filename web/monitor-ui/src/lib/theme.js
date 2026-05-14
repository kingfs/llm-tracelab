export const THEME_KEY = "llm-tracelab.monitor.theme";

export const themeOptions = [
  { value: "system", label: "System", short: "S" },
  { value: "dark", label: "Dark", short: "D" },
  { value: "light", label: "Light", short: "L" },
];

export function currentTheme() {
  return window.localStorage.getItem(THEME_KEY) || "system";
}

export function applyTheme(theme = currentTheme()) {
  const normalized = themeOptions.some((option) => option.value === theme) ? theme : "system";
  document.documentElement.dataset.theme = normalized;
  return normalized;
}
