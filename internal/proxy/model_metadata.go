package proxy

import (
	"strings"

	llmspecs "github.com/kingfs/go-llm-specs"
)

func newAggregatedModelListEntry(model string) aggregatedModelListEntry {
	entry := aggregatedModelListEntry{
		ID:      model,
		Object:  "model",
		OwnedBy: "llm-tracelab",
	}

	spec, ok := llmspecs.Get(model)
	if !ok {
		return entry
	}

	contextLength := spec.ContextLength()
	maxOutput := spec.MaxOutput()
	entry.CanonicalSlug = spec.ID()
	entry.Name = spec.Name()
	entry.Provider = spec.Provider()
	entry.Description = spec.Description()
	entry.Summary = spec.Summary()
	entry.Family = spec.Family()
	entry.Series = spec.Series()
	entry.Tags = spec.Tags()
	entry.ContextLength = contextLength
	entry.MaxModelLen = contextLength
	entry.MaxOutput = maxOutput
	entry.Architecture = modelArchitecture(spec.Features())
	entry.TopProvider = &aggregatedModelTopProvider{
		ContextLength:       positiveIntPtr(contextLength),
		MaxCompletionTokens: positiveIntPtr(maxOutput),
	}
	return entry
}

func modelArchitecture(features llmspecs.Capability) *aggregatedModelArchitecture {
	inputs := modalities(features, []capabilityModality{
		{llmspecs.ModalityTextIn, "text"},
		{llmspecs.ModalityImageIn, "image"},
		{llmspecs.ModalityAudioIn, "audio"},
		{llmspecs.ModalityVideoIn, "video"},
		{llmspecs.ModalityFileIn, "file"},
	})
	outputs := modalities(features, []capabilityModality{
		{llmspecs.ModalityTextOut, "text"},
		{llmspecs.ModalityImageOut, "image"},
		{llmspecs.ModalityAudioOut, "audio"},
		{llmspecs.ModalityVideoOut, "video"},
		{llmspecs.ModalityFileOut, "file"},
	})
	if len(inputs) == 0 && len(outputs) == 0 {
		return nil
	}

	return &aggregatedModelArchitecture{
		Modality:         strings.Join(inputs, "+") + "->" + strings.Join(outputs, "+"),
		InputModalities:  inputs,
		OutputModalities: outputs,
	}
}

type capabilityModality struct {
	capability llmspecs.Capability
	name       string
}

func modalities(features llmspecs.Capability, candidates []capabilityModality) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if features.Has(candidate.capability) {
			out = append(out, candidate.name)
		}
	}
	return out
}

func positiveIntPtr(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}
