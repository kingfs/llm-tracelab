package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

type Options struct {
	OutputDir string
	RewriteV2 bool
	RebuildDB bool
}

type Result struct {
	ScannedFiles     int
	ConvertedFiles   int
	SkippedV3Files   int
	RebuiltIndexRows int
}

func Run(st *store.Store, opts Options) (Result, error) {
	if opts.OutputDir == "" {
		return Result{}, fmt.Errorf("output dir is required")
	}
	if st == nil {
		return Result{}, fmt.Errorf("store is required")
	}

	var result Result
	if opts.RewriteV2 {
		if err := filepath.Walk(opts.OutputDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(info.Name(), ".http") {
				return nil
			}

			result.ScannedFiles++

			converted, err := convertFile(path)
			if err != nil {
				return err
			}
			if converted {
				result.ConvertedFiles++
			} else {
				result.SkippedV3Files++
			}
			return nil
		}); err != nil {
			return result, err
		}
	}

	if opts.RebuildDB {
		rows, err := st.Rebuild()
		if err != nil {
			return result, err
		}
		result.RebuiltIndexRows = rows
	}

	return result, nil
}

func convertFile(path string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}

	if parsed.Header.Version == "LLM_PROXY_V3" {
		return false, nil
	}

	if parsed.PayloadOffset > int64(len(content)) {
		return false, fmt.Errorf("invalid payload offset in %s", path)
	}

	header := parsed.Header
	header.Version = "LLM_PROXY_V3"
	events := parsed.Events
	if len(events) == 0 {
		events = recordfile.BuildEvents(header)
	}

	prelude, err := recordfile.MarshalPrelude(header, events)
	if err != nil {
		return false, fmt.Errorf("marshal %s: %w", path, err)
	}

	payload := content[parsed.PayloadOffset:]
	updated := make([]byte, 0, len(prelude)+len(payload))
	updated = append(updated, prelude...)
	updated = append(updated, payload...)

	if err := os.WriteFile(path, updated, 0o644); err != nil {
		return false, err
	}

	return true, nil
}
