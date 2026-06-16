package proxy

import "testing"

func TestNewAggregatedModelListEntryEnrichesAliasFromSpecs(t *testing.T) {
	entry := newAggregatedModelListEntry("qwen3.6-35b-a3b")

	if entry.ID != "qwen3.6-35b-a3b" {
		t.Fatalf("ID = %q, want qwen3.6-35b-a3b", entry.ID)
	}
	if entry.CanonicalSlug != "qwen/qwen3.6-35b-a3b" {
		t.Fatalf("CanonicalSlug = %q, want qwen/qwen3.6-35b-a3b", entry.CanonicalSlug)
	}
	if entry.Name == "" {
		t.Fatalf("Name is empty")
	}
	if entry.ContextLength != 262144 {
		t.Fatalf("ContextLength = %d, want 262144", entry.ContextLength)
	}
	if entry.MaxModelLen != 262144 {
		t.Fatalf("MaxModelLen = %d, want 262144", entry.MaxModelLen)
	}
	if entry.MaxOutput != 65536 {
		t.Fatalf("MaxOutput = %d, want 65536", entry.MaxOutput)
	}
	if entry.TopProvider == nil || entry.TopProvider.ContextLength == nil || *entry.TopProvider.ContextLength != 262144 {
		t.Fatalf("TopProvider.ContextLength = %#v, want 262144", entry.TopProvider)
	}
	if entry.TopProvider.MaxCompletionTokens == nil || *entry.TopProvider.MaxCompletionTokens != 65536 {
		t.Fatalf("TopProvider.MaxCompletionTokens = %#v, want 65536", entry.TopProvider.MaxCompletionTokens)
	}
	if entry.Architecture == nil || entry.Architecture.Modality != "text+image+video->text" {
		t.Fatalf("Architecture = %#v, want text+image+video->text", entry.Architecture)
	}
}

func TestNewAggregatedModelListEntryKeepsUnknownModelMinimal(t *testing.T) {
	entry := newAggregatedModelListEntry("local-only-model")

	if entry.ID != "local-only-model" {
		t.Fatalf("ID = %q, want local-only-model", entry.ID)
	}
	if entry.Object != "model" {
		t.Fatalf("Object = %q, want model", entry.Object)
	}
	if entry.OwnedBy != "llm-tracelab" {
		t.Fatalf("OwnedBy = %q, want llm-tracelab", entry.OwnedBy)
	}
	if entry.ContextLength != 0 || entry.CanonicalSlug != "" || entry.TopProvider != nil {
		t.Fatalf("unknown model was unexpectedly enriched: %#v", entry)
	}
}
