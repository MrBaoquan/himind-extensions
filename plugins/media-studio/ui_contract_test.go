package main

import (
	"os"
	"strings"
	"testing"
)

func readUIAsset(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(content)
}

func requireContains(t *testing.T, content, needle, scope string) {
	t.Helper()
	if !strings.Contains(content, needle) {
		t.Fatalf("%s: missing %q", scope, needle)
	}
}

func requireAbsent(t *testing.T, content, needle, scope string) {
	t.Helper()
	if strings.Contains(content, needle) {
		t.Fatalf("%s: unexpected %q", scope, needle)
	}
}

// TestStudioUIContract 把视图的几条硬约束钉在测试里：入口文件、桥接调用、
// 四种降级状态、可访问性与视觉令牌。视图是唯一没有后端测试覆盖的部分，
// 一旦被顺手改坏，用户看到的是白屏而不是报错。
func TestStudioUIContract(t *testing.T) {
	html := readUIAsset(t, "ui/index.html")
	js := readUIAsset(t, "ui/app.js")
	css := readUIAsset(t, "ui/style.css")
	manifest := readUIAsset(t, "plugin.json")

	t.Run("entry files are wired", func(t *testing.T) {
		requireContains(t, manifest, `"entry": "ui/index.html"`, "manifest")
		requireContains(t, html, `href="style.css"`, "html")
		requireContains(t, html, `src="app.js"`, "html")
		requireContains(t, html, `<meta charset="utf-8" />`, "html")
	})

	t.Run("every declared capability the view calls exists in the manifest", func(t *testing.T) {
		for _, capability := range []string{
			capRuntimeStatus, capStyleList, capStyleSave,
			capBatchStart, capBatchStatus, capBatchCancel, capBatchList,
			capAssetLineage,
		} {
			requireContains(t, js, capability, "view capability")
			requireContains(t, manifest, capability, "manifest capability")
		}
	})

	t.Run("bridge commands are limited to the two host commands", func(t *testing.T) {
		requireContains(t, js, "'invoke_plugin_view_capability'", "bridge")
		requireContains(t, js, "'get_plugin_view_context'", "bridge")
		requireAbsent(t, js, "window.open", "bridge")
		requireAbsent(t, js, "eval(", "bridge")
		if count := strings.Count(js, "internals.invoke("); count != 2 {
			t.Fatalf("bridge: 宿主调用应集中在两个入口函数里，实际出现 %d 次", count)
		}
	})

	t.Run("degradation states are explicit", func(t *testing.T) {
		for _, phrase := range []string{
			"需从 Agent 打开",
			"缺少生图服务",
			"没有绑定工作区",
			"生成失败",
			"单次矩阵最多 36 张",
			"读取风格包失败",
		} {
			requireContains(t, js, phrase, "degradation")
		}
		requireContains(t, js, "bridge_unavailable", "degradation")
	})

	t.Run("accessibility affordances exist", func(t *testing.T) {
		requireContains(t, html, `aria-live="polite"`, "a11y")
		requireContains(t, html, `role="progressbar"`, "a11y")
		requireContains(t, html, `aria-valuenow="0"`, "a11y")
		requireContains(t, html, `role="dialog"`, "a11y")
		requireContains(t, html, `aria-modal="true"`, "a11y")
		requireContains(t, js, "function trapFocus(event, container)", "a11y")
		requireContains(t, js, "event.key === 'Escape'", "a11y")
		requireContains(t, js, "aria-pressed", "a11y")
		requireContains(t, js, "'true' : 'false'", "a11y")
	})

	t.Run("visual tokens and restrained effects", func(t *testing.T) {
		for _, token := range []string{"--color-bg:", "--color-surface:", "--color-primary:", "--space-4:", "--radius-md:", "--control-height:", "--shadow-float:"} {
			requireContains(t, css, token, "design tokens")
		}
		for _, forbidden := range []string{"linear-gradient", "radial-gradient", "text-shadow", "drop-shadow"} {
			requireAbsent(t, css, forbidden, "visual restraint")
		}
		for _, breakpoint := range []string{"max-width: 980px", "max-width: 680px", "max-width: 420px"} {
			requireContains(t, css, breakpoint, "responsive")
		}
		requireContains(t, css, "prefers-reduced-motion", "motion")
	})

	t.Run("matrix preview and history are present", func(t *testing.T) {
		requireContains(t, html, `id="matrix-preview"`, "composer")
		requireContains(t, html, `id="progress-fill"`, "stage")
		requireContains(t, html, `id="history-list"`, "stage")
		requireContains(t, html, `id="lineage"`, "lineage")
		requireContains(t, js, "STATE_LABEL", "stage")
		requireContains(t, js, "schedulePoll", "polling")
	})
}
