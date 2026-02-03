package replayselector

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SelectFile 根据回放根目录、请求 path 与 body 中的 model，按目录约定选一个 .http 文件。
// 约定：优先 replayDir/<model>/*.http（按文件名排序取第一个），若无则 replayDir/*.http（扁平）。
// path 暂未参与选择，预留后续按 path 细分（如 /v1/models）。
func SelectFile(replayDir, path, model string) (string, error) {
	replayDir = filepath.Clean(replayDir)
	if model != "" {
		modelDir := filepath.Join(replayDir, model)
		if st, err := os.Stat(modelDir); err == nil && st.IsDir() {
			if p, err := pickFirstHTTP(modelDir); err == nil {
				return p, nil
			}
		}
	}
	// 回退：扁平目录
	if p, err := pickFirstHTTP(replayDir); err == nil {
		return p, nil
	}
	return "", errors.New("replayselector: no .http file found")
}

// pickFirstHTTP 在 dir 下找所有 .http 文件，按文件名排序后返回第一个；若无则返回 error。
func pickFirstHTTP(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".http") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", errors.New("no .http in dir")
	}
	sort.Strings(names)
	return filepath.Join(dir, names[0]), nil
}
