package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 本测试复刻 ADR 0119 的机械可判定部分（字符集/段数/长度/版本后缀/相邻段重复），
// 与 HiMind Agent 构建候选包时的门禁保持一致，避免再次出现“本地能装、生产构建被拒”。
func capabilityNamingViolations(id string) []string {
	violations := []string{}
	if id == "" || len(id) > 48 {
		violations = append(violations, "长度超出上限 48")
	}
	if strings.Contains(id, "_") {
		violations = append(violations, "不得使用下划线")
	}
	segments := strings.Split(id, ".")
	if len(segments) < 2 || len(segments) > 5 {
		violations = append(violations, "段数必须为 2–5")
	}
	for _, segment := range segments {
		valid := segment != "" && segment[0] >= 'a' && segment[0] <= 'z' && !strings.HasSuffix(segment, "-")
		for _, ch := range segment {
			if !(ch >= 'a' && ch <= 'z') && !(ch >= '0' && ch <= '9') && ch != '-' {
				valid = false
			}
		}
		if !valid {
			violations = append(violations, "每段必须小写、以字母开头、只含字母/数字/连字符")
			break
		}
	}
	if last := segments[len(segments)-1]; len(last) > 1 && last[0] == 'v' && strings.Trim(last[1:], "0123456789") == "" {
		violations = append(violations, "版本不得进入能力 ID")
	}
	for i := 0; i+1 < len(segments); i++ {
		parent := strings.Split(segments[i], "-")
		child := strings.Split(segments[i+1], "-")
		if segments[i] == segments[i+1] {
			violations = append(violations, "不得出现重复段")
			continue
		}
		for _, token := range child {
			for _, existing := range parent {
				if token == existing {
					violations = append(violations, "子段与父段语义重复: "+segments[i]+" → "+segments[i+1])
				}
			}
		}
	}
	return violations
}

func TestManifestCapabilityIDsFollowAdr0119(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest struct {
		ID                  string `json:"id"`
		CapabilityNamespace string `json:"capability_namespace"`
		Capabilities        []struct {
			ID string `json:"id"`
		} `json:"capabilities"`
		Commands []struct {
			ID string `json:"id"`
		} `json:"commands"`
		Views []struct {
			ExternalCapabilities []string `json:"external_capabilities"`
		} `json:"views"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	// 与内核一致：声明了 capability_namespace 就以它为准，否则退化为插件 ID 的 slug。
	namespace := manifest.CapabilityNamespace
	if namespace == "" {
		parts := strings.Split(manifest.ID, ".")
		namespace = parts[len(parts)-1]
	}
	if namespace != "media-library" {
		t.Fatalf("capability_namespace 应为 media-library，实际 %q", namespace)
	}
	seen := map[string]bool{}
	check := func(id, kind string) {
		if !strings.HasPrefix(id, namespace+".") {
			t.Errorf("%s %s 必须以能力命名空间 %s. 开头", kind, id, namespace)
		}
		for _, violation := range capabilityNamingViolations(id) {
			t.Errorf("%s %s 未通过 ADR 0119 命名门禁: %s", kind, id, violation)
		}
		if seen[id] {
			t.Errorf("%s %s 重复声明", kind, id)
		}
		seen[id] = true
	}
	for _, capability := range manifest.Capabilities {
		check(capability.ID, "能力")
	}
	for _, command := range manifest.Commands {
		check(command.ID, "命令")
	}
	for _, view := range manifest.Views {
		for _, id := range view.ExternalCapabilities {
			check(id, "视图外链能力")
		}
	}
	for legacy := range legacyCapabilityIDs {
		if seen[legacy] {
			t.Errorf("旧 ID %s 不得再写进清单", legacy)
		}
	}
}
