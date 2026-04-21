export function buildToolMessageSummary(message, declaredTools = []) {
  if (message.tool_calls?.length) {
    const summaries = message.tool_calls.map((call) => {
      const name = call.function?.name || "tool";
      const match = findDeclaredToolForCall(call, declaredTools);
      return match?.name ? `${name} matched declared tool` : name;
    });
    return `Tools: ${summaries.join(", ")}`;
  }
  if (message.message_type === "tool_result") {
    const label = message.name || message.tool_call_id || "tool result";
    return `Tool result: ${label}`;
  }
  return "";
}

export function getDeclaredToolName(tool = {}) {
  return (
    tool.name ||
    tool.function?.name ||
    tool.function_name ||
    tool.tool_name ||
    tool.title ||
    ""
  );
}

export function getDeclaredToolDescription(tool = {}) {
  return tool.description || tool.function?.description || "";
}

export function getDeclaredToolParameters(tool = {}) {
  return (
    tool.parameters ||
    tool.function?.parameters ||
    tool.input_schema ||
    tool.inputSchema ||
    ""
  );
}

export function normalizeDeclaredTool(tool = {}, index = 0) {
  const name = getDeclaredToolName(tool) || `tool_${index + 1}`;
  const source = tool.source || (tool.input_schema || tool.inputSchema ? "anthropic" : tool.function ? "openai" : tool.type || "tool");
  return {
    ...tool,
    id: tool.id || `${name || "tool"}-${index}`,
    type: tool.type || "function",
    source,
    name,
    description: getDeclaredToolDescription(tool),
    parameters: getDeclaredToolParameters(tool),
  };
}

export function collectTraceToolCalls(detail) {
  if (!detail) {
    return [];
  }
  const calls = [];
  for (const message of detail.messages || []) {
    for (const call of message.tool_calls || []) {
      calls.push(call);
    }
  }
  for (const call of detail.tool_calls || []) {
    calls.push(call);
  }
  return calls;
}

export function normalizeToolName(value = "") {
  return String(value || "").trim().toLowerCase();
}

export function isSameToolName(left = "", right = "") {
  return normalizeToolName(left) !== "" && normalizeToolName(left) === normalizeToolName(right);
}

export function findDeclaredToolForCall(call, declaredTools = []) {
  const name = call?.function?.name || "";
  return declaredTools.find((tool) => isSameToolName(getDeclaredToolName(tool), name)) || null;
}

export function countToolMatches(toolCalls = [], toolName = "") {
  return toolCalls.filter((call) => isSameToolName(call.function?.name, toolName)).length;
}

export function buildToolSchemaSummary(parameters = "") {
  if (!parameters) {
    return "No schema";
  }
  try {
    const payload = JSON.parse(parameters);
    const properties = payload?.properties ? Object.keys(payload.properties) : [];
    if (properties.length) {
      return `${properties.length} field${properties.length > 1 ? "s" : ""}: ${properties.slice(0, 3).join(", ")}${properties.length > 3 ? "..." : ""}`;
    }
    return payload?.type ? `Schema type ${payload.type}` : "JSON schema";
  } catch {
    return "Schema available";
  }
}
