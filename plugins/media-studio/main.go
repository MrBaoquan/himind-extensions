// media-studio —— HiMind 媒体创作工具。
//
// 内核已经提供原子能力：生图（media.image.generate）、改图（media.image.edit）、
// 视频与语音。缺的不是「再调一次模型」，而是把生成组织成可复现交付的那一层：
//
//	风格包     —— 提示词骨架、负向描述、尺寸与 Provider 参数固化下来
//	矩阵作业   —— 一句话 × N 风格 × M 尺寸，异步跑、逐项留结果
//	生成血缘   —— 每个产物都能反查到当时的提示词、风格包与模型
//	改图入口   —— 对已有素材（含媒体资源库里的素材）按描述改图
//
// 插件不保管任何 Provider 密钥：所有模型调用都通过 Agent 注入的能力代理，
// 由内核按用户配置的服务与路由执行，本插件只在 manifest 里声明依赖哪些能力。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"himind-plugin/media-studio/internal/himindjsonrpc"
)

// 能力 ID。首段 media 是 ADR 0119 注册域，第二段 studio 是本插件的命名空间。
const (
	capRuntimeStatus = "media.studio.runtime.status"
	capStyleList     = "media.studio.style.list"
	capStyleSave     = "media.studio.style.save"
	capStyleRemove   = "media.studio.style.remove"
	capBatchStart    = "media.studio.batch.start"
	capBatchStatus   = "media.studio.batch.status"
	capBatchCancel   = "media.studio.batch.cancel"
	capBatchList     = "media.studio.batch.list"
	capAssetLineage  = "media.studio.asset.lineage"
	capImageRework   = "media.studio.image.rework"
)

// service 把所有状态显式挂在结构体上：单测可以注入假的 invoker，
// 生产环境则惰性从环境变量构造 broker。
type service struct {
	invoker CapabilityInvoker
	jobs    *jobManager
}

func defaultService() *service {
	return &service{jobs: newJobManager()}
}

func (s *service) capabilityInvoker() (CapabilityInvoker, error) {
	if s.invoker != nil {
		return s.invoker, nil
	}
	created, err := brokerFromEnv()
	if err != nil {
		return nil, err
	}
	return created, nil
}

func main() {
	if err := himindjsonrpc.Serve(os.Stdin, os.Stdout, defaultService().handle); err != nil {
		fmt.Fprintln(os.Stderr, "media-studio:", err)
		os.Exit(1)
	}
}

func (s *service) handle(request himindjsonrpc.Request) (any, *himindjsonrpc.Error) {
	switch request.Method {
	case capRuntimeStatus:
		return s.runtimeStatus()

	case capStyleList:
		var input styleListInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.styleList(input)

	case capStyleSave:
		var input styleSaveInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.styleSave(input)

	case capStyleRemove:
		var input styleRemoveInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.styleRemove(input)

	case capBatchStart:
		var input batchStartInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.batchStart(input)

	case capBatchStatus:
		var input jobStatusInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.batchStatus(input)

	case capBatchCancel:
		var input jobCancelInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.batchCancel(input)

	case capBatchList:
		var input jobListInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.batchList(input)

	case capAssetLineage:
		var input lineageInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.assetLineage(input)

	case capImageRework:
		var input reworkInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.imageRework(input)
	}

	return nil, &himindjsonrpc.Error{
		Code:    -32601,
		Message: fmt.Sprintf("未知能力: %s", request.Method),
	}
}

// ── 输入结构 ──────────────────────────────────────────

type styleListInput struct {
	Kind           string `json:"kind"`
	IncludeBuiltin *bool  `json:"include_builtin"`
	IncludeCustom  *bool  `json:"include_custom"`
}

type styleSaveInput struct {
	Style Style `json:"style"`
}

type styleRemoveInput struct {
	ID string `json:"id"`
}

type batchStartInput struct {
	WorkspaceRoot  string         `json:"workspace_root"`
	Prompt         string         `json:"prompt"`
	Styles         []string       `json:"styles"`
	Sizes          []string       `json:"sizes"`
	Variants       int            `json:"variants"`
	OutputDir      string         `json:"output_dir"`
	FilenamePrefix string         `json:"filename_prefix"`
	Service        string         `json:"service"`
	Model          string         `json:"model"`
	Parameters     map[string]any `json:"parameters"`
	// Wait 为真时阻塞到作业收敛或超时，返回完整结果。
	// 工作流步骤拿不到「稍后再查」的机会，就需要这个同步形态。
	Wait        bool `json:"wait"`
	WaitSeconds int  `json:"wait_seconds"`
}

type jobStatusInput struct {
	JobID        string `json:"job_id"`
	IncludeItems *bool  `json:"include_items"`
}

type jobCancelInput struct {
	JobID string `json:"job_id"`
}

type jobListInput struct {
	Limit int `json:"limit"`
}

type lineageInput struct {
	MediaID      string `json:"media_id"`
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Limit        int    `json:"limit"`
	IncludeChain bool   `json:"include_chain"`
}

type reworkInput struct {
	WorkspaceRoot  string         `json:"workspace_root"`
	InputImages    []string       `json:"input_images"`
	Prompt         string         `json:"prompt"`
	StyleID        string         `json:"style_id"`
	Count          int            `json:"count"`
	Size           string         `json:"size"`
	OutputDir      string         `json:"output_dir"`
	FilenamePrefix string         `json:"filename_prefix"`
	Service        string         `json:"service"`
	Model          string         `json:"model"`
	Parameters     map[string]any `json:"parameters"`
}

// ── 能力实现 ──────────────────────────────────────────

func (s *service) runtimeStatus() (any, *himindjsonrpc.Error) {
	root, err := dataRoot()
	if err != nil {
		return nil, himindjsonrpc.Failed(err.Error())
	}
	styles, styleErr := allStyles()
	custom, _ := loadCustomStyles()
	payload := map[string]any{
		"data_root":        root,
		"broker_available": strings.TrimSpace(os.Getenv(brokerURLEnv)) != "",
		"builtin_styles":   len(builtinStyles()),
		"custom_styles":    len(custom),
		"styles_total":     len(styles),
		"running_jobs":     s.jobs.runningCount(),
		"dependencies":     []string{capImageGenerate, capImageEdit, capProviderCatalog},
		"capabilities":     []string{capStyleList, capStyleSave, capBatchStart, capImageRework},
	}
	if styleErr != nil {
		payload["styles_error"] = styleErr.Error()
	}
	invoker, brokerErr := s.capabilityInvoker()
	if brokerErr != nil {
		payload["ready"] = false
		payload["message"] = brokerErr.Error()
		return payload, nil
	}
	catalog, err := invoker.Invoke(capProviderCatalog, map[string]any{})
	if err != nil {
		payload["ready"] = true
		payload["provider_state"] = "unknown"
		payload["provider_message"] = err.Error()
		return payload, nil
	}
	providers := imageProviders(catalog)
	payload["providers"] = providers
	payload["provider_state"] = "ready"
	payload["ready"] = len(providers) > 0
	if len(providers) == 0 {
		payload["message"] = "本机没有已绑定生图能力的 AI 服务：先在 Agent 的 AI 服务里为某个服务绑定 image.generate"
	}
	return payload, nil
}

// imageProviders 从内核 Provider 目录里挑出真正可用的生图/改图服务。
//
// 目录结构属于内核契约，插件只读它需要的字段；任何字段缺失都退化为「未知」，
// 不让状态查询因为一次结构变化整体失败。
func imageProviders(catalog map[string]any) []map[string]any {
	raw, ok := catalog["providers"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		bindings, ok := entry["media_capabilities"].([]any)
		if !ok {
			continue
		}
		supports := false
		for _, binding := range bindings {
			capability, ok := binding.(map[string]any)
			if !ok {
				continue
			}
			name := stringValue(capability["capability"])
			if name == "image.generate" || name == "image.edit" {
				supports = true
				break
			}
		}
		if !supports {
			continue
		}
		health := "unknown"
		if detail, ok := entry["health"].(map[string]any); ok {
			if value := stringValue(detail["state"]); value != "" {
				health = value
			}
		}
		result = append(result, map[string]any{
			"id":           stringValue(entry["id"]),
			"display_name": stringValue(entry["display_name"]),
			"health":       health,
			"has_api_key":  entry["has_api_key"] == true,
		})
	}
	return result
}

func (s *service) styleList(input styleListInput) (any, *himindjsonrpc.Error) {
	styles, err := allStyles()
	if err != nil {
		return nil, himindjsonrpc.Failed(err.Error())
	}
	includeBuiltin := input.IncludeBuiltin == nil || *input.IncludeBuiltin
	includeCustom := input.IncludeCustom == nil || *input.IncludeCustom
	kind := strings.TrimSpace(input.Kind)
	result := make([]Style, 0, len(styles))
	for _, style := range styles {
		if style.Builtin && !includeBuiltin {
			continue
		}
		if !style.Builtin && !includeCustom {
			continue
		}
		result = append(result, style)
	}
	if kind != "" && kind != "image" {
		// 目前只做图像风格包；其它类型显式返回空集，避免调用方误以为没配好。
		result = nil
	}
	return map[string]any{"styles": result, "total": len(result), "kind": firstNonEmpty(kind, "image")}, nil
}

func (s *service) styleSave(input styleSaveInput) (any, *himindjsonrpc.Error) {
	style, err := normalizeStyle(input.Style)
	if err != nil {
		return nil, himindjsonrpc.InvalidParams(err.Error())
	}
	custom, err := loadCustomStyles()
	if err != nil {
		return nil, himindjsonrpc.Failed(err.Error())
	}
	now := nowText()
	replaced := false
	for index, existing := range custom {
		if existing.ID != style.ID {
			continue
		}
		style.CreatedAt = firstNonEmpty(existing.CreatedAt, now)
		style.UpdatedAt = now
		custom[index] = style
		replaced = true
		break
	}
	if !replaced {
		style.CreatedAt = now
		style.UpdatedAt = now
		custom = append(custom, style)
	}
	if err := saveCustomStyles(custom); err != nil {
		return nil, himindjsonrpc.Failed(err.Error())
	}
	return map[string]any{"style": style, "created": !replaced, "total": len(custom)}, nil
}

func (s *service) styleRemove(input styleRemoveInput) (any, *himindjsonrpc.Error) {
	id := strings.ToLower(strings.TrimSpace(input.ID))
	if id == "" {
		return nil, himindjsonrpc.InvalidParams("id 不能为空")
	}
	custom, err := loadCustomStyles()
	if err != nil {
		return nil, himindjsonrpc.Failed(err.Error())
	}
	kept := make([]Style, 0, len(custom))
	removed := false
	for _, style := range custom {
		if style.ID == id {
			removed = true
			continue
		}
		kept = append(kept, style)
	}
	if !removed {
		if _, ok := styleIndex()[id]; ok {
			return nil, himindjsonrpc.InvalidParams("内置风格包不能删除；保存同 ID 的自定义风格包即可覆盖它的效果")
		}
		return nil, himindjsonrpc.InvalidParams("风格包不存在: " + id)
	}
	if err := saveCustomStyles(kept); err != nil {
		return nil, himindjsonrpc.Failed(err.Error())
	}
	return map[string]any{"removed": id, "remaining": len(kept)}, nil
}

func (s *service) batchStart(input batchStartInput) (any, *himindjsonrpc.Error) {
	workspace, err := cleanWorkspace(input.WorkspaceRoot)
	if err != nil {
		return nil, himindjsonrpc.InvalidParams(err.Error())
	}
	basePrompt := strings.TrimSpace(input.Prompt)
	if basePrompt == "" {
		return nil, himindjsonrpc.InvalidParams("prompt 不能为空：矩阵需要一句话描述要生成什么")
	}
	if len([]rune(basePrompt)) > 4000 {
		return nil, himindjsonrpc.InvalidParams("prompt 不能超过 4000 个字符")
	}
	styleIDs := make([]string, 0, len(input.Styles))
	styles := make([]Style, 0, len(input.Styles))
	for _, id := range input.Styles {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		style, ok, styleErr := findStyle(trimmed)
		if styleErr != nil {
			return nil, himindjsonrpc.Failed(styleErr.Error())
		}
		if !ok {
			return nil, himindjsonrpc.InvalidParams("风格包不存在: " + trimmed + "（先用 media.studio.style.list 查看）")
		}
		styleIDs = append(styleIDs, style.ID)
		styles = append(styles, style)
	}
	if len(styles) == 0 {
		styles = append(styles, Style{ID: ""})
		styleIDs = append(styleIDs, "")
	}
	sizes := normalizeSizes(input.Sizes)
	if len(sizes) > 4 {
		sizes = sizes[:4]
	}
	if len(sizes) == 0 {
		seen := map[string]bool{}
		for _, style := range styles {
			for _, size := range style.Sizes {
				if !seen[size] {
					seen[size] = true
					sizes = append(sizes, size)
				}
			}
		}
		if len(sizes) == 0 {
			sizes = []string{"1024x1024"}
		}
	}
	variants := input.Variants
	if variants <= 0 {
		variants = 1
	}
	if variants > 4 {
		variants = 4
	}
	cells := len(styles) * len(sizes)
	if cells*variants > maxMatrixItems {
		return nil, himindjsonrpc.InvalidParams(fmt.Sprintf(
			"本次矩阵会产生 %d 张图片，超过单次上限 %d 张；请减少风格包、尺寸或变体数",
			cells*variants, maxMatrixItems))
	}
	outputDir, err := resolveOutputDir(workspace, input.OutputDir)
	if err != nil {
		return nil, himindjsonrpc.InvalidParams(err.Error())
	}
	items := make([]*jobItem, 0, cells)
	index := 0
	for _, style := range styles {
		for _, size := range sizes {
			items = append(items, &jobItem{
				Index:   index,
				StyleID: style.ID,
				Size:    size,
				Prompt:  composePrompt(basePrompt, style),
				State:   itemStatePending,
			})
			index++
		}
	}
	plan := batchPlan{
		WorkspaceRoot: workspace,
		OutputDir:     outputDir,
		BasePrompt:    basePrompt,
		StyleIDs:      styleIDs,
		Sizes:         sizes,
		Variants:      variants,
		ServiceID:     strings.TrimSpace(input.Service),
		Model:         strings.TrimSpace(input.Model),
		Params:        input.Parameters,
		FilenameBase:  firstNonEmpty(strings.TrimSpace(input.FilenamePrefix), "studio-"+timestampSuffix()),
		Items:         items,
	}
	invoker, brokerErr := s.capabilityInvoker()
	if brokerErr != nil {
		return nil, himindjsonrpc.Failed(brokerErr.Error())
	}
	job, err := s.jobs.start(invoker, plan)
	if err != nil {
		return nil, himindjsonrpc.Failed(err.Error())
	}
	summary := map[string]any{
		"job_id":          job.ID,
		"state":           job.State,
		"total_cells":     len(items),
		"images_per_cell": variants,
		"images_total":    len(items) * variants,
		"output_dir":      outputDir,
		"prompt":          basePrompt,
		"styles":          styleIDs,
		"sizes":           sizes,
		"message":         fmt.Sprintf("已开始矩阵生成：%d 个单元格 × %d 张，共 %d 张", len(items), variants, len(items)*variants),
	}
	if !input.Wait {
		return summary, nil
	}
	return s.waitForJob(job.ID, input.WaitSeconds, summary), nil
}

// waitForJob 把异步作业收敛成一次同步返回，供工作流步骤使用。
//
// 超时不等于失败：已完成的单元格仍然返回，状态保持实际值并附一句说明，
// 这样调用方不会把「还没跑完」误判成「全都没成」。
func (s *service) waitForJob(jobID string, waitSeconds int, summary map[string]any) map[string]any {
	limit := waitSeconds
	if limit <= 0 {
		limit = 300
	}
	if limit > 1800 {
		limit = 1800
	}
	deadline := time.Now().Add(time.Duration(limit) * time.Second)
	for {
		snapshot, ok := s.jobs.snapshot(jobID)
		if !ok {
			summary["message"] = "作业在执行期间丢失，请用 media.studio.batch.status 复查"
			return summary
		}
		if isTerminalState(snapshot.State) {
			files := make([]any, 0, 8)
			for _, item := range snapshot.Items {
				for _, file := range item.Files {
					files = append(files, file)
				}
			}
			summary["job"] = snapshot
			summary["state"] = snapshot.State
			summary["completed"] = snapshot.Completed
			summary["failed"] = snapshot.Failed
			summary["files"] = files
			summary["file_count"] = len(files)
			summary["message"] = fmt.Sprintf("矩阵生成结束：成功 %d/%d，失败 %d，产物 %d 个",
				snapshot.Completed-snapshot.Failed, snapshot.Total, snapshot.Failed, len(files))
			return summary
		}
		if time.Now().After(deadline) {
			summary["job"] = snapshot
			summary["state"] = snapshot.State
			summary["completed"] = snapshot.Completed
			summary["failed"] = snapshot.Failed
			summary["message"] = fmt.Sprintf("等待 %d 秒后仍未跑完，可用 media.studio.batch.status 继续查看", limit)
			return summary
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func isTerminalState(value string) bool {
	switch value {
	case jobStateSucceeded, jobStatePartial, jobStateFailed, jobStateCanceled, jobStateInterrupted:
		return true
	}
	return false
}

func (s *service) batchStatus(input jobStatusInput) (any, *himindjsonrpc.Error) {
	jobID := strings.TrimSpace(input.JobID)
	if jobID == "" {
		return nil, himindjsonrpc.InvalidParams("job_id 不能为空")
	}
	job, ok := s.jobs.snapshot(jobID)
	if !ok {
		return nil, himindjsonrpc.InvalidParams("作业不存在: " + jobID)
	}
	includeItems := input.IncludeItems == nil || *input.IncludeItems
	if !includeItems {
		job.Items = nil
	}
	percent := 0
	if job.Total > 0 {
		percent = job.Completed * 100 / job.Total
	}
	return map[string]any{
		"job":       job,
		"job_id":    job.ID,
		"state":     job.State,
		"total":     job.Total,
		"completed": job.Completed,
		"failed":    job.Failed,
		"percent":   percent,
	}, nil
}

func (s *service) batchCancel(input jobCancelInput) (any, *himindjsonrpc.Error) {
	jobID := strings.TrimSpace(input.JobID)
	if jobID == "" {
		return nil, himindjsonrpc.InvalidParams("job_id 不能为空")
	}
	job, ok := s.jobs.cancel(jobID)
	if !ok {
		return nil, himindjsonrpc.InvalidParams("作业不存在: " + jobID)
	}
	return map[string]any{"job_id": job.ID, "state": job.State, "completed": job.Completed, "total": job.Total}, nil
}

func (s *service) batchList(input jobListInput) (any, *himindjsonrpc.Error) {
	jobs := s.jobs.list(input.Limit)
	return map[string]any{"jobs": jobs, "total": len(jobs), "running": s.jobs.runningCount()}, nil
}

func (s *service) assetLineage(input lineageInput) (any, *himindjsonrpc.Error) {
	if strings.TrimSpace(input.MediaID) == "" && strings.TrimSpace(input.Path) == "" && strings.TrimSpace(input.SHA256) == "" {
		return nil, himindjsonrpc.InvalidParams("至少给出 media_id、path 或 sha256 之一")
	}
	matches, chain, err := queryLineage(lineageQuery{
		MediaID: input.MediaID,
		Path:    input.Path,
		SHA256:  input.SHA256,
		Limit:   input.Limit,
		Chain:   input.IncludeChain,
	})
	if err != nil {
		return nil, himindjsonrpc.Failed(err.Error())
	}
	payload := map[string]any{"records": matches, "total": len(matches), "found": len(matches) > 0}
	if len(chain) > 0 {
		payload["chain"] = chain
	}
	if len(matches) == 0 {
		payload["message"] = "没有这条素材的生成记录：它可能来自在线素材库、手工导入或其它工具"
	}
	return payload, nil
}

// imageRework 是媒体资源库「AI 处理」入口的落点：对一张已有图片按描述改图。
//
// 与矩阵的区别是**输入是已有素材**，所以必须处理好三种来源：工作区路径、
// media:<id>、以及资源库/共享库里的工作区外路径。工作区外的文件先复制进
// 工作区再改，避免内核的「参考图必须在会话工作区内」约束把入口堵死。
func (s *service) imageRework(input reworkInput) (any, *himindjsonrpc.Error) {
	references := make([]string, 0, len(input.InputImages))
	for _, value := range input.InputImages {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		references = append(references, trimmed)
	}
	if len(references) == 0 {
		return nil, himindjsonrpc.InvalidParams("input_images 不能为空：需要至少一张参考图")
	}
	if len(references) > 4 {
		return nil, himindjsonrpc.InvalidParams("一次最多 4 张参考图")
	}
	workspace := ""
	if strings.TrimSpace(input.WorkspaceRoot) != "" {
		resolved, err := cleanWorkspace(input.WorkspaceRoot)
		if err != nil {
			return nil, himindjsonrpc.InvalidParams(err.Error())
		}
		workspace = resolved
	}
	styleID := strings.TrimSpace(input.StyleID)
	style := Style{}
	if styleID != "" {
		found, ok, err := findStyle(styleID)
		if err != nil {
			return nil, himindjsonrpc.Failed(err.Error())
		}
		if !ok {
			return nil, himindjsonrpc.InvalidParams("风格包不存在: " + styleID)
		}
		style = found
	}
	prompt := composePrompt(input.Prompt, style)
	if strings.TrimSpace(prompt) == "" {
		return nil, himindjsonrpc.InvalidParams("prompt 与 style_id 至少要给一个：改图需要描述改成什么样")
	}
	if len([]rune(prompt)) > 8000 {
		return nil, himindjsonrpc.InvalidParams("prompt 不能超过 8000 个字符")
	}
	count := input.Count
	if count <= 0 {
		count = 1
	}
	if count > 4 {
		count = 4
	}
	outputDir, err := resolveOutputDir(workspace, input.OutputDir)
	if err != nil {
		return nil, himindjsonrpc.InvalidParams(err.Error())
	}
	prepared, parentMediaID, err := s.prepareInputs(workspace, references)
	if err != nil {
		return nil, himindjsonrpc.Failed(err.Error())
	}
	invoker, brokerErr := s.capabilityInvoker()
	if brokerErr != nil {
		return nil, himindjsonrpc.Failed(brokerErr.Error())
	}
	request := map[string]any{
		"prompt":       prompt,
		"input_images": prepared,
		"n":            count,
		"output_dir":   outputDir,
		"inline":       false,
		"filename":     firstNonEmpty(strings.TrimSpace(input.FilenamePrefix), "rework-"+timestampSuffix()),
	}
	if size := strings.TrimSpace(input.Size); size != "" {
		if sizes := normalizeSizes([]string{size}); len(sizes) > 0 {
			request["size"] = sizes[0]
		}
	}
	if service := strings.TrimSpace(input.Service); service != "" {
		request["service"] = service
	}
	if model := strings.TrimSpace(input.Model); model != "" {
		request["model"] = model
	}
	if len(input.Parameters) > 0 {
		request["parameters"] = input.Parameters
	}
	result, err := invoker.Invoke(capImageEdit, request)
	if err != nil {
		return nil, himindjsonrpc.Failed(err.Error())
	}
	files := filesFromResult(result)
	if len(files) == 0 {
		return nil, himindjsonrpc.Failed("改图能力没有返回任何文件")
	}
	records := make([]Lineage, 0, len(files))
	for _, file := range files {
		records = append(records, Lineage{
			LineageID:     newID("lineage"),
			MediaID:       file.MediaID,
			Path:          file.Path,
			SHA256:        file.SHA256,
			MimeType:      file.MimeType,
			Width:         file.Width,
			Height:        file.Height,
			SizeBytes:     file.SizeBytes,
			Capability:    capImageEdit,
			Prompt:        prompt,
			BasePrompt:    strings.TrimSpace(input.Prompt),
			StyleID:       style.ID,
			ParentMediaID: parentMediaID,
			ServiceID:     stringValue(result["service_id"]),
			Model:         stringValue(result["model"]),
			RequestID:     stringValue(result["request_id"]),
			WorkspaceRoot: workspace,
			CreatedAt:     nowText(),
		})
	}
	if err := appendLineage(records); err != nil {
		// 血缘写失败不该让已经花钱生成的图片消失：把产物照常返回，附带告警。
		return map[string]any{
			"files":      files,
			"prompt":     prompt,
			"style_id":   style.ID,
			"output_dir": outputDir,
			"warning":    "生成成功但血缘写入失败: " + err.Error(),
		}, nil
	}
	return map[string]any{
		"files":      files,
		"prompt":     prompt,
		"style_id":   style.ID,
		"service_id": stringValue(result["service_id"]),
		"model":      stringValue(result["model"]),
		"request_id": stringValue(result["request_id"]),
		"output_dir": outputDir,
		"lineage":    records,
	}, nil
}

// prepareInputs 归一化参考图：工作区内路径原样使用，工作区外文件先复制进工作区。
func (s *service) prepareInputs(workspace string, references []string) ([]string, string, error) {
	prepared := make([]string, 0, len(references))
	parentMediaID := ""
	for _, reference := range references {
		if strings.HasPrefix(reference, "media:") {
			if parentMediaID == "" {
				parentMediaID = strings.TrimPrefix(reference, "media:")
			}
			prepared = append(prepared, reference)
			continue
		}
		path := reference
		if !filepath.IsAbs(path) {
			if workspace == "" {
				return nil, "", fmt.Errorf("相对路径需要 workspace_root 才能解析: %s", reference)
			}
			resolved, err := resolveInside(workspace, reference)
			if err != nil {
				return nil, "", err
			}
			path = resolved
		}
		if workspace == "" || fileInside(workspace, path) {
			prepared = append(prepared, path)
			continue
		}
		copied, err := copyIntoWorkspace(workspace, path)
		if err != nil {
			return nil, "", err
		}
		prepared = append(prepared, copied)
	}
	return prepared, parentMediaID, nil
}

// copyIntoWorkspace 把工作区外的素材复制到 .himind/media/studio-inputs/。
//
// 这样做的直接原因是内核改图要求参考图位于会话工作区内；顺带也让一次改图
// 不会因为原素材被移出资源库而断链。
func copyIntoWorkspace(workspace, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("参考图不存在或不可读: %s（%v）", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("参考图不是文件: %s", path)
	}
	targetDir := filepath.Join(workspace, ".himind", "media", "studio-inputs")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("创建输入目录失败: %w", err)
	}
	name := filepath.Base(path)
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	target := filepath.Join(targetDir, sanitizeFilename(stem)+"-"+randomHex(4)+filepath.Ext(name))
	source, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("读取参考图失败: %w", err)
	}
	defer func() { _ = source.Close() }()
	destination, err := os.Create(target)
	if err != nil {
		return "", fmt.Errorf("写入参考图失败: %w", err)
	}
	defer func() { _ = destination.Close() }()
	if _, err := destination.ReadFrom(source); err != nil {
		return "", fmt.Errorf("复制参考图失败: %w", err)
	}
	return target, nil
}

// resolveOutputDir 解析产物目录：缺省落在工作区 .himind/media，相对路径按工作区解析。
func resolveOutputDir(workspace, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		if workspace == "" {
			root, err := dataSub("outputs")
			if err != nil {
				return "", err
			}
			return root, nil
		}
		value = defaultOutputDir(workspace)
	}
	if workspace != "" {
		resolved, err := resolveInside(workspace, value)
		if err != nil {
			return "", err
		}
		value = resolved
	} else if !filepath.IsAbs(value) {
		return "", fmt.Errorf("没有 workspace_root 时 output_dir 必须是绝对路径: %s", value)
	}
	if err := os.MkdirAll(value, 0o755); err != nil {
		return "", fmt.Errorf("创建产物目录失败: %w", err)
	}
	return value, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
