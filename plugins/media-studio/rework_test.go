package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path string, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
	return path
}

func TestImageReworkComposesPromptAndRecordsLineage(t *testing.T) {
	service := newTestService(t)
	workspace := t.TempDir()
	source := writeTestFile(t, filepath.Join(workspace, "hall.png"), "fake-image")
	invoker := &fakeInvoker{files: 2}
	service.invoker = invoker

	result, rpcError := service.imageRework(reworkInput{
		WorkspaceRoot: workspace,
		InputImages:   []string{source},
		Prompt:        "把背景换成夜色城市",
		StyleID:       "night-city",
		Count:         2,
	})
	if rpcError != nil {
		t.Fatalf("改图失败: %s", rpcError.Message)
	}
	payload := result.(map[string]any)
	if len(payload["files"].([]generatedFile)) != 2 {
		t.Fatalf("应返回 2 个产物: %+v", payload)
	}
	prompt := payload["prompt"].(string)
	if !strings.Contains(prompt, "把背景换成夜色城市") || !strings.Contains(prompt, "风格要求：") {
		t.Fatalf("最终提示词没有合并主体描述与风格: %q", prompt)
	}

	if len(invoker.calls) != 1 {
		t.Fatalf("应只调用一次改图: %d", len(invoker.calls))
	}
	call := invoker.calls[0]
	if capability := invoker.capabilities[0]; capability != capImageEdit {
		t.Fatalf("应调用 media.image.edit: %s", capability)
	}
	images := call["input_images"].([]string)
	if images[0] != source {
		t.Fatalf("工作区内参考图应原样传入: %+v", images)
	}
	if call["inline"] != false {
		t.Fatal("插件调用必须关掉 inline，否则 base64 会撑爆代理响应")
	}

	records, err := loadLineage()
	if err != nil {
		t.Fatalf("读取血缘失败: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("每个产物都应记录血缘: %d", len(records))
	}
	if records[0].StyleID != "night-city" || records[0].Capability != capImageEdit {
		t.Fatalf("血缘内容不对: %+v", records[0])
	}
}

func TestImageReworkCopiesAssetOutsideWorkspace(t *testing.T) {
	service := newTestService(t)
	workspace := t.TempDir()
	outside := writeTestFile(t, filepath.Join(t.TempDir(), "library", "shot.png"), "external")
	invoker := &fakeInvoker{files: 1}
	service.invoker = invoker

	if _, rpcError := service.imageRework(reworkInput{
		WorkspaceRoot: workspace,
		InputImages:   []string{outside},
		Prompt:        "换成夜景",
	}); rpcError != nil {
		t.Fatalf("改图失败: %s", rpcError.Message)
	}
	images := invoker.calls[0]["input_images"].([]string)
	if len(images) != 1 {
		t.Fatalf("参考图数量不对: %+v", images)
	}
	if !fileInside(workspace, images[0]) {
		t.Fatalf("工作区外素材应先复制进工作区: %s", images[0])
	}
	if !strings.Contains(images[0], "studio-inputs") {
		t.Fatalf("复制的参考图应落在 studio-inputs 下: %s", images[0])
	}
	if _, err := os.Stat(images[0]); err != nil {
		t.Fatalf("复制后的参考图不存在: %v", err)
	}
}

func TestImageReworkPassesMediaReferenceAndParent(t *testing.T) {
	service := newTestService(t)
	service.invoker = &fakeInvoker{files: 1}
	result, rpcError := service.imageRework(reworkInput{
		InputImages: []string{"media:media_abc"},
		Prompt:      "加一层暖光",
	})
	if rpcError != nil {
		t.Fatalf("改图失败: %s", rpcError.Message)
	}
	images := service.invoker.(*fakeInvoker).calls[0]["input_images"].([]string)
	if images[0] != "media:media_abc" {
		t.Fatalf("media 引用应原样透传: %+v", images)
	}
	records, err := loadLineage()
	if err != nil {
		t.Fatalf("读取血缘失败: %v", err)
	}
	if len(records) != 1 || records[0].ParentMediaID != "media_abc" {
		t.Fatalf("应记录父素材: %+v", records)
	}
	if result.(map[string]any)["output_dir"] == "" {
		t.Fatal("没有工作区时应退回插件数据目录作为产物目录")
	}
}

func TestImageReworkValidatesInput(t *testing.T) {
	service := newTestService(t)
	service.invoker = &fakeInvoker{}
	if _, rpcError := service.imageRework(reworkInput{Prompt: "x"}); rpcError == nil {
		t.Fatal("缺参考图应被拒绝")
	}
	workspace := t.TempDir()
	source := writeTestFile(t, filepath.Join(workspace, "a.png"), "x")
	if _, rpcError := service.imageRework(reworkInput{
		WorkspaceRoot: workspace,
		InputImages:   []string{source},
	}); rpcError == nil {
		t.Fatal("既没有 prompt 也没有风格包应被拒绝")
	}
	if _, rpcError := service.imageRework(reworkInput{
		WorkspaceRoot: workspace,
		InputImages:   []string{source},
		Prompt:        "x",
		StyleID:       "missing-style",
	}); rpcError == nil {
		t.Fatal("未知风格包应被拒绝")
	}
}

func TestImageReworkClampsCount(t *testing.T) {
	service := newTestService(t)
	service.invoker = &fakeInvoker{files: 1}
	workspace := t.TempDir()
	source := writeTestFile(t, filepath.Join(workspace, "a.png"), "x")
	if _, rpcError := service.imageRework(reworkInput{
		WorkspaceRoot: workspace,
		InputImages:   []string{source},
		Prompt:        "x",
		Count:         99,
	}); rpcError != nil {
		t.Fatalf("改图失败: %s", rpcError.Message)
	}
	if got := service.invoker.(*fakeInvoker).calls[0]["n"]; got != 4 {
		t.Fatalf("张数应被夹到 4: %v", got)
	}
}

func TestReworkKeepsOutputsInsideWorkspace(t *testing.T) {
	service := newTestService(t)
	service.invoker = &fakeInvoker{files: 1}
	workspace := t.TempDir()
	source := writeTestFile(t, filepath.Join(workspace, "a.png"), "x")
	if _, rpcError := service.imageRework(reworkInput{
		WorkspaceRoot: workspace,
		InputImages:   []string{source},
		Prompt:        "x",
		OutputDir:     filepath.Join(workspace, "..", "escape"),
	}); rpcError == nil {
		t.Fatal("产物目录越出工作区应被拒绝")
	}
}
