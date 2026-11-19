package main

import (
	"path"
	"runtime"
	"sort"
	"strings"
)

// projectPath 是登记表里持久化的路径映射。
type projectPath struct {
	Path   string `json:"path"`
	Kind   string `json:"kind,omitempty"`   // repo | workdir | svn | release | ...
	Label  string `json:"label,omitempty"`  // 人类可读的说明
	Source string `json:"source,omitempty"` // local | workbench | declared
}

// normalizePathKey 把路径归一到可比较的键：统一分隔符、去掉 Windows 长路径前缀、
// 清理 ./ 与 ..；Windows 上大小写不敏感。
func normalizePathKey(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	for {
		switch {
		case strings.HasPrefix(p, "//?/"):
			p = p[4:]
		case strings.HasPrefix(p, "//./"):
			p = p[4:]
		default:
			goto cleaned
		}
	}
cleaned:
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// pathMatchScore 给「会话路径 dirKey」与「映射路径 mapKey」算命中分：
// 相等最高，子路径按映射路径长度评分（越长越具体、越优先）；不命中返回 -1。
func pathMatchScore(dirKey, mapKey string) int {
	if dirKey == "" || mapKey == "" {
		return -1
	}
	if dirKey == mapKey {
		return len(mapKey) + 1_000_000
	}
	prefix := strings.TrimSuffix(mapKey, "/") + "/"
	if strings.HasPrefix(dirKey, prefix) {
		return len(mapKey)
	}
	return -1
}

func pathsToMaps(paths []projectPath) []map[string]any {
	out := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		out = append(out, map[string]any{
			"path":   p.Path,
			"kind":   p.Kind,
			"label":  p.Label,
			"source": p.Source,
		})
	}
	return out
}

// pendingPathStat 汇总一个「待认领」路径的条目情况。
type pendingPathStat struct {
	count int
	days  map[string]struct{}
	first string
	last  string
	hints map[string]int
}

// collectPendingPaths 汇总未归类（有 repo_path 但项目为空）的条目，按路径聚合。
func collectPendingPaths(entries []entry) []map[string]any {
	stats := map[string]*pendingPathStat{}
	display := map[string]string{}
	for _, item := range entries {
		key := normalizePathKey(item.RepoPath)
		if key == "" || strings.TrimSpace(item.Project) != "" {
			continue
		}
		stat := stats[key]
		if stat == nil {
			stat = &pendingPathStat{days: map[string]struct{}{}, hints: map[string]int{}}
			stats[key] = stat
			display[key] = strings.TrimSpace(item.RepoPath)
		}
		stat.count++
		stat.days[item.Date] = struct{}{}
		if stat.first == "" || item.Date < stat.first {
			stat.first = item.Date
		}
		if item.Date > stat.last {
			stat.last = item.Date
		}
		if hint := strings.TrimSpace(item.ProjectHint); hint != "" {
			stat.hints[hint]++
		}
	}
	out := make([]map[string]any, 0, len(stats))
	for key, stat := range stats {
		hint := ""
		best := 0
		for h, c := range stat.hints {
			if c > best || (c == best && h < hint) {
				hint, best = h, c
			}
		}
		out = append(out, map[string]any{
			"path":         display[key],
			"path_key":     key,
			"entry_count":  stat.count,
			"active_days":  len(stat.days),
			"first_date":   stat.first,
			"last_date":    stat.last,
			"project_hint": hint,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if mapInt(out[i]["entry_count"]) != mapInt(out[j]["entry_count"]) {
			return mapInt(out[i]["entry_count"]) > mapInt(out[j]["entry_count"])
		}
		return mapString(out[i]["path"]) < mapString(out[j]["path"])
	})
	return out
}
