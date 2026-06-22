package runtime

import "github.com/kingfs/llm-tracelab/internal/responses/protocol"

func cloneResponseValue(resp protocol.Response) protocol.Response {
	resp.Output = cloneOutputItems(resp.Output)
	resp.Metadata = cloneMap(resp.Metadata)
	return resp
}

func cloneInputItems(items []protocol.InputItem) []protocol.InputItem {
	if len(items) == 0 {
		return nil
	}
	copied := make([]protocol.InputItem, len(items))
	for i, item := range items {
		copied[i] = cloneInputItem(item)
	}
	return copied
}

func cloneInputItem(item protocol.InputItem) protocol.InputItem {
	item.Content = append([]protocol.ContentPart(nil), item.Content...)
	return item
}

func cloneOutputItems(items []protocol.OutputItem) []protocol.OutputItem {
	if len(items) == 0 {
		return nil
	}
	copied := make([]protocol.OutputItem, len(items))
	for i, item := range items {
		copied[i] = cloneOutputItem(item)
	}
	return copied
}

func cloneOutputItem(item protocol.OutputItem) protocol.OutputItem {
	item.Content = append([]protocol.ContentPart(nil), item.Content...)
	item.Action = cloneMap(item.Action)
	return item
}

func cloneMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	copied := make(map[string]any, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}
