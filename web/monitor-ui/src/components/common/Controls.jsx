import React from "react";

export function Switch({ checked, onChange, disabled = false, label = "", title = "" }) {
  return (
    <button
      className={checked ? "switch-control switch-control-on" : "switch-control"}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label || (checked ? "Enabled" : "Disabled")}
      title={title || label}
      disabled={disabled}
      onClick={(event) => {
        event.preventDefault();
        event.stopPropagation();
        onChange?.(!checked);
      }}
    >
      <span />
    </button>
  );
}
