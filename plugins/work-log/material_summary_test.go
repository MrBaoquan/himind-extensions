package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSummaryMaterialPreservesSessionIdentityAndFitsNoToolsPrompt(t *testing.T) {
	rows := make([]materialDigest, 22)
	for i := range rows {
		rows[i] = materialDigest{
			Source: "codex", SessionID: fmt.Sprintf("session-%02d", i), Date: "2026-11-17",
			Title: "修复日志入库", UserPrompts: []string{strings.Repeat("工作需求", 200)},
			AssistantExcerpts: []string{"诊断中", strings.Repeat("已验证写入", 200)},
			Evidence:          []map[string]any{{"message_id": "internal-evidence"}},
		}
	}
	data, err := summaryMaterial(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > summaryMaterialBytes || strings.Contains(string(data), `"entries"`) || strings.Contains(string(data), "internal-evidence") {
		t.Fatalf("unexpected AI artifact, %d bytes", len(data))
	}
	var input summaryInput
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	if len(input.Sessions) != len(rows) || len(input.Sessions) > summaryMaterialSessions || len(input.Sessions)+input.OmittedCount != len(rows) {
		t.Fatalf("unexpected coverage: %d included, %d omitted", len(input.Sessions), input.OmittedCount)
	}
	for i, row := range input.Sessions {
		if row.Source != rows[i].Source || row.SessionID != rows[i].SessionID || row.Date != rows[i].Date || row.Title != rows[i].Title || row.Request == "" || !strings.HasPrefix(row.Result, "已验证写入") {
			t.Fatalf("source facts were lost for row %d: %+v", i, row)
		}
	}
}

func TestSummaryRequestSkipsBootstrapAndRetainsWorkPrompts(t *testing.T) {
	role := "【你的角色】implementer " + strings.Repeat("约束", 100) + " 任务：修复日志漏项。验收：幂等补齐。"
	prompts := []string{"<environment_context><cwd>F:/work</cwd></environment_context>", role, "修复后按稳定会话 ID 重跑并确认日报无重复。", "You are a subagent spawned by another session. 审查实现并给出结论。", "好的，请开始推进", "继续", "请构建拓展并进行真实日志补跑，测试"}
	got := summaryRequest(prompts)
	if strings.Contains(got, "environment_context") || strings.Contains(got, "subagent spawned") || strings.Contains(got, "implementer") {
		t.Fatalf("bootstrap leaked into request: %q", got)
	}
	if !strings.Contains(got, "任务：修复日志漏项") || !strings.Contains(got, "稳定会话 ID") || !strings.Contains(got, "构建拓展") {
		t.Fatalf("meaningful work prompts were dropped: %q", got)
	}
	if len([]rune(got)) > summaryRequestRunes {
		t.Fatalf("request exceeded rune budget: %d", len([]rune(got)))
	}
}

func TestSummaryRequestKeepsMainWorkAfterAcknowledgements(t *testing.T) {
	prompts := []string{"好的，请开始推进", "请构建拓展并进行真实日志补跑，测试", "继续", "继续", "已完全放行，继续", "工作日志采集器计划退役废弃，请考虑可行性", "把会话采集能力增加客户端支持，放到 Agent 侧是否合理？", "好的，现在把工作日志直接走 Agent 服务并跑通流程。", "请在正式生产 Agent 环境中跑通日志工作流", "那请补充 Agent 的激活能力", "工作日志工作流现在是否跑通", "需要在生产环境跑通并验收。"}
	got := summaryRequest(prompts)
	if got == "" || !strings.Contains(got, "构建拓展") || !strings.Contains(got, "生产环境") {
		t.Fatalf("main session request was lost: %q", got)
	}
}

func TestSummarySourceRetainsPromptsBeforeDeterministicEvidenceBudget(t *testing.T) {
	digest := materialDigest{
		Source: "codex", SessionID: "session-main", Date: "2026-11-18",
		Title:             "工作日志采集迁移",
		UserPrompts:       []string{"好的，请开始推进", "请构建拓展并进行真实日志补跑，测试", "继续", "需要在生产环境跑通并验收。"},
		AssistantExcerpts: []string{"已完成 Agent 会话采集接入和生产验收。"},
	}
	aiSource := summarySourceDigest(digest)
	trimMaterialDigest(&digest, 1, 8)
	if len(digest.UserPrompts) >= len(aiSource.UserPrompts) {
		t.Fatal("fixture did not exercise deterministic budget trimming")
	}
	data, err := summaryMaterial([]materialDigest{aiSource})
	if err != nil {
		t.Fatal(err)
	}
	var got summaryInput
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) != 1 || !strings.Contains(got.Sessions[0].Request, "真实日志补跑") || !strings.Contains(got.Sessions[0].Request, "生产环境") {
		t.Fatalf("AI summary lost prompts beyond deterministic evidence budget: %+v", got.Sessions)
	}
}
