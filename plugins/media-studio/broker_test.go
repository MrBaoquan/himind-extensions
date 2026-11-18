package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestFilesFromResultReadsTopLevelAndNested(t *testing.T) {
	topLevel := map[string]any{
		"files": []any{map[string]any{"media_id": "m1", "path": `C:\a.png`, "width": float64(512)}},
	}
	files := filesFromResult(topLevel)
	if len(files) != 1 || files[0].MediaID != "m1" || files[0].Width != 512 {
		t.Fatalf("顶层 files 解析失败: %+v", files)
	}

	nested := map[string]any{
		"structuredContent": map[string]any{
			"files": []any{map[string]any{"media_id": "m2", "path": `C:\b.png`}},
		},
	}
	files = filesFromResult(nested)
	if len(files) != 1 || files[0].MediaID != "m2" {
		t.Fatalf("嵌套 structuredContent 解析失败: %+v", files)
	}

	if got := filesFromResult(map[string]any{"files": []any{map[string]any{"media_id": "no-path"}}}); got != nil {
		t.Fatalf("缺 path 的产物应被丢弃: %+v", got)
	}
	if got := filesFromResult(map[string]any{}); got != nil {
		t.Fatalf("没有 files 字段应返回 nil: %+v", got)
	}
}

func TestBrokerFromEnvRequiresInjectedEndpoint(t *testing.T) {
	t.Setenv(brokerURLEnv, "")
	t.Setenv(brokerTokenEnv, "")
	if _, err := brokerFromEnv(); err == nil {
		t.Fatal("没有 Agent 注入的代理地址时应报错")
	}
	t.Setenv(brokerURLEnv, "http://127.0.0.1:1/invoke")
	t.Setenv(brokerTokenEnv, "token")
	created, err := brokerFromEnv()
	if err != nil {
		t.Fatalf("注入后应能构造 broker: %v", err)
	}
	if created.url != "http://127.0.0.1:1/invoke" || created.token != "token" {
		t.Fatalf("broker 配置不对: %+v", created)
	}
}

func TestBrokerInvokeSurfacesCapabilityFailureMessage(t *testing.T) {
	// 用一个必然连不上的地址，断言错误信息带上能力名，便于用户定位是哪一步失败。
	t.Setenv(brokerURLEnv, "http://127.0.0.1:1/invoke")
	t.Setenv(brokerTokenEnv, "token")
	created, err := brokerFromEnv()
	if err != nil {
		t.Fatalf("构造 broker 失败: %v", err)
	}
	_, err = created.Invoke(capImageGenerate, map[string]any{"prompt": "x"})
	if err == nil {
		t.Fatal("连不上代理时应返回错误")
	}
	if !strings.Contains(err.Error(), capImageGenerate) {
		t.Fatalf("错误信息应包含能力 ID: %v", err)
	}
}

func TestServiceWithoutBrokerReportsActionableError(t *testing.T) {
	service := newTestService(t)
	_, rpcError := service.batchStart(batchStartInput{
		WorkspaceRoot: t.TempDir(),
		Prompt:        "x",
	})
	if rpcError == nil || !strings.Contains(rpcError.Message, "能力代理") {
		t.Fatalf("没有代理时应给出可执行的说明: %+v", rpcError)
	}
}

func TestRuntimeStatusWithoutBrokerIsReadable(t *testing.T) {
	service := newTestService(t)
	result, rpcError := service.runtimeStatus()
	if rpcError != nil {
		t.Fatalf("状态查询不该失败: %s", rpcError.Message)
	}
	payload := result.(map[string]any)
	if payload["broker_available"] != false {
		t.Fatalf("未注入代理时应报告不可用: %+v", payload)
	}
	if payload["ready"] != false {
		t.Fatalf("不可用时 ready 应为 false: %+v", payload)
	}
	if _, ok := payload["builtin_styles"].(int); !ok {
		t.Fatalf("状态应包含内置风格包数量: %+v", payload)
	}
}

func TestRuntimeStatusReadsProviderCatalog(t *testing.T) {
	service := newTestService(t)
	service.invoker = &fakeInvoker{}
	// fakeInvoker 返回的是生图结果而不是目录：目录里没有 providers 时，
	// 状态必须是「目录读到了、但没有可用服务」，而不是整体失败。
	result, rpcError := service.runtimeStatus()
	if rpcError != nil {
		t.Fatalf("状态查询失败: %s", rpcError.Message)
	}
	payload := result.(map[string]any)
	if payload["provider_state"] != "ready" {
		t.Fatalf("目录可读时应报告 ready: %+v", payload)
	}
	if providers, ok := payload["providers"].([]map[string]any); !ok || len(providers) != 0 {
		t.Fatalf("没有 providers 时应返回空列表: %+v", payload["providers"])
	}
	if payload["ready"] != false || payload["message"] == "" {
		t.Fatalf("没有可用服务时应说明怎么配: %+v", payload)
	}
}

func TestImageProvidersFiltersByBinding(t *testing.T) {
	catalog := map[string]any{
		"providers": []any{
			map[string]any{
				"id":           "ark",
				"display_name": "火山方舟",
				"has_api_key":  true,
				"health":       map[string]any{"state": "ready"},
				"media_capabilities": []any{
					map[string]any{"capability": "image.generate"},
				},
			},
			map[string]any{
				"id":                 "chat-only",
				"display_name":       "只做对话",
				"media_capabilities": []any{},
			},
		},
	}
	providers := imageProviders(catalog)
	if len(providers) != 1 {
		t.Fatalf("只应保留绑定了生图能力的服务: %+v", providers)
	}
	if providers[0]["id"] != "ark" || providers[0]["health"] != "ready" {
		t.Fatalf("服务信息不对: %+v", providers[0])
	}
}

func TestCapabilityResultJSONIsEncodable(t *testing.T) {
	service := newTestService(t)
	result, rpcError := service.styleList(styleListInput{})
	if rpcError != nil {
		t.Fatalf("风格包查询失败: %s", rpcError.Message)
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatalf("结果不可编码: %v", err)
	}
}

func TestStyleSaveAndRemoveRoundTrip(t *testing.T) {
	service := newTestService(t)
	saved, rpcError := service.styleSave(styleSaveInput{Style: Style{
		ID:           "my-pack",
		Label:        "自定义包",
		PromptSuffix: "柔和逆光",
		Sizes:        []string{"1024x1024"},
	}})
	if rpcError != nil {
		t.Fatalf("保存失败: %s", rpcError.Message)
	}
	if saved.(map[string]any)["created"] != true {
		t.Fatalf("首次保存应标记为新建: %+v", saved)
	}
	listed, _ := service.styleList(styleListInput{})
	styles := listed.(map[string]any)["styles"].([]Style)
	found := false
	for _, style := range styles {
		if style.ID == "my-pack" {
			found = true
		}
	}
	if !found {
		t.Fatal("保存后应能在列表里看到自定义包")
	}
	if _, rpcError := service.styleRemove(styleRemoveInput{ID: "clean-product"}); rpcError == nil {
		t.Fatal("内置风格包不应被删除")
	}
	if _, rpcError := service.styleRemove(styleRemoveInput{ID: "my-pack"}); rpcError != nil {
		t.Fatalf("自定义包应能删除: %s", rpcError.Message)
	}
}

func TestLineageCapabilityRequiresSomeKey(t *testing.T) {
	service := newTestService(t)
	if _, rpcError := service.assetLineage(lineageInput{}); rpcError == nil {
		t.Fatal("没有查询键应被拒绝")
	}
	result, rpcError := service.assetLineage(lineageInput{MediaID: "missing"})
	if rpcError != nil {
		t.Fatalf("查不到不该报错: %s", rpcError.Message)
	}
	if result.(map[string]any)["found"] != false {
		t.Fatalf("应返回 found=false: %+v", result)
	}
}

func TestJobStatusRequiresExistingJob(t *testing.T) {
	service := newTestService(t)
	if _, rpcError := service.batchStatus(jobStatusInput{}); rpcError == nil {
		t.Fatal("缺 job_id 应被拒绝")
	}
	if _, rpcError := service.batchStatus(jobStatusInput{JobID: "nope"}); rpcError == nil {
		t.Fatal("不存在的作业应被拒绝")
	}
	if _, rpcError := service.batchCancel(jobCancelInput{JobID: "nope"}); rpcError == nil {
		t.Fatal("取消不存在的作业应被拒绝")
	}
}

func TestPluginManifestDeclaresEveryCapability(t *testing.T) {
	content, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatalf("读取清单失败: %v", err)
	}
	var manifest struct {
		ID                  string   `json:"id"`
		CapabilityNamespace string   `json:"capability_namespace"`
		CapabilityDeps      []string `json:"capability_dependencies"`
		Capabilities        []struct {
			ID string `json:"id"`
		} `json:"capabilities"`
		Contributes struct {
			Commands []struct {
				ID string `json:"id"`
			} `json:"commands"`
			Views []struct {
				ExternalCapabilities []string `json:"external_capabilities"`
			} `json:"views"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("清单不是合法 JSON: %v", err)
	}
	declared := map[string]bool{}
	for _, capability := range manifest.Capabilities {
		declared[capability.ID] = true
		if !strings.HasPrefix(capability.ID, manifest.CapabilityNamespace+".") {
			t.Fatalf("能力 %s 不在声明的命名空间 %s 下", capability.ID, manifest.CapabilityNamespace)
		}
	}
	for _, expected := range []string{
		capRuntimeStatus, capStyleList, capStyleSave, capStyleRemove,
		capBatchStart, capBatchStatus, capBatchCancel, capBatchList,
		capAssetLineage, capImageRework,
	} {
		if !declared[expected] {
			t.Fatalf("清单缺少能力声明: %s", expected)
		}
	}
	commanded := map[string]bool{}
	for _, command := range manifest.Contributes.Commands {
		commanded[command.ID] = true
	}
	for id := range declared {
		if !commanded[id] {
			t.Fatalf("能力 %s 没有对应的命令入口", id)
		}
	}
	// 视图要调用自己插件的能力，不需要 external_capabilities；
	// 这里反过来确认没有多要权限。
	for _, view := range manifest.Contributes.Views {
		if len(view.ExternalCapabilities) > 0 {
			t.Fatalf("媒体创作工具的视图不应申请外部能力: %v", view.ExternalCapabilities)
		}
	}
	deps := map[string]bool{}
	for _, dep := range manifest.CapabilityDeps {
		deps[dep] = true
	}
	for _, expected := range []string{capImageGenerate, capImageEdit, capProviderCatalog} {
		if !deps[expected] {
			t.Fatalf("清单缺少能力依赖: %s", expected)
		}
	}
}
