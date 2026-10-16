package iostudio

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 集成测试：调用**真实的** IOStudio 命令行，验证端到端链路。
//
// 本机没有 IOStudio 时跳过。可用 IOSTUDIO_CLI 指定要用哪一份 ——
// 这一点很关键：设备表就在该安装目录下，所以"用哪一份 IOStudio"同时决定了
// "拿哪份设备宣告做校验"。
func locateOrSkip(t *testing.T) CLI {
	t.Helper()
	cli, err := Locate(os.Getenv("IOSTUDIO_CLI"))
	if err != nil {
		t.Skipf("未找到 IOStudio 命令行，跳过集成测试: %v", err)
	}
	return cli
}

// 先读设备表、再按真实声明生成规格 —— 这正是预期的工作流：
// 不猜通道名，而是从设备的自述里取。
func buildSpec(t *testing.T, deviceName, channel string) string {
	t.Helper()
	spec := map[string]any{
		"name":        "集成测试",
		"duration_ms": 2000,
		"fps":         60,
		"device":      deviceName,
		"tracks": []map[string]any{{
			"role": "motion", "channel": channel, "output_type": "oaxis",
			"label": "测试轨道", "value_type": "float", "neutral": 0.5, "engineering_range": 60,
		}},
		"presets": []map[string]any{{
			"id": "p", "name": "测试预设", "category": "测试", "duration_ms": 2000,
			"channels": []map[string]any{{
				"role": "motion",
				"keyframes": []map[string]any{
					{"time_ms": 0, "value": 0.5, "interpolation": "ease_in_out"},
					{"time_ms": 2000, "value": 0.6, "interpolation": "ease_in_out"},
				},
			}},
		}},
		"cues": []map[string]any{{"time_ms": 0, "preset": "p", "intensity": 1.0}},
	}
	payload, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "spec.json")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// 规格构建 + 产物预检走完整链路。设备表可用时必须是"真的通过"。
func TestIntegrationBuildSpecProducesValidMotion(t *testing.T) {
	cli := locateOrSkip(t)
	if cli.DeviceTable == "" {
		t.Skip("该 IOStudio 安装没有设备表，无法生成能通过预检的规格")
	}
	table, err := ReadDeviceTable(cli.ConfigDir, cli.DeviceTable)
	if err != nil {
		t.Skipf("读取设备表失败，跳过: %v", err)
	}

	// 找一个"设备已声明且有可用动作"的组合。
	var deviceName, channel string
	for _, device := range table.Devices {
		for _, action := range device.OActions {
			if action.Usable && len(action.Keys) > 0 {
				deviceName, channel = device.Name, action.Keys[0]
				break
			}
		}
		if deviceName != "" {
			break
		}
	}
	if deviceName == "" {
		t.Skipf("设备表里没有可用动作（%d 台设备）", len(table.Devices))
	}

	specPath := buildSpec(t, deviceName, channel)
	outPath := filepath.Join(filepath.Dir(specPath), "out.motion")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := Build(ctx, BuildRequest{
		CLI:           cli,
		WorkspaceRoot: filepath.Dir(specPath),
		SpecPath:      specPath,
		OutPath:       outPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("构建应通过（设备=%s 通道=%s）: exit=%d stdout=%s stderr=%s issues=%+v",
			deviceName, channel, result.ExitCode, result.Stdout, result.Stderr, result.Validation)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("应产出动作文件: %v", err)
	}

	// 用了哪份工具与设备表必须回显 —— 否则不匹配时毫无线索。
	if result.CLI == "" || result.ConfigDir == "" || result.DeviceTable == "" {
		t.Fatalf("结果应回显 cli / config_dir / device_table: %+v", result)
	}
}

// 干跑必须真的不留下产物（它是"先看方案"的关口，不是"偷偷落地"）。
func TestIntegrationPlanLeavesNoArtifact(t *testing.T) {
	cli := locateOrSkip(t)
	if cli.DeviceTable == "" {
		t.Skip("该 IOStudio 安装没有设备表")
	}
	table, err := ReadDeviceTable(cli.ConfigDir, cli.DeviceTable)
	if err != nil || len(table.Devices) == 0 {
		t.Skip("没有可用设备表")
	}
	deviceName := table.Devices[0].Name
	channel := "OAxis_01"
	for _, action := range table.Devices[0].OActions {
		if action.Usable && len(action.Keys) > 0 {
			channel = action.Keys[0]
			break
		}
	}

	specPath := buildSpec(t, deviceName, channel)
	dir := filepath.Dir(specPath)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := Build(ctx, BuildRequest{
		CLI:           cli,
		WorkspaceRoot: dir,
		SpecPath:      specPath,
		DryRun:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MotionPath != "" {
		t.Fatalf("干跑不应回显产物路径: %s", result.MotionPath)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".motion" {
			t.Fatalf("干跑留下了产物: %s", entry.Name())
		}
	}
}

// 设备表里没有该设备时，必须报出"设备未声明"，而不是含糊地说构建失败。
// 这条守住的是本项目最在意的一类问题：静默不匹配。
func TestIntegrationUndeclaredDeviceIsReportedPrecisely(t *testing.T) {
	cli := locateOrSkip(t)
	specPath := buildSpec(t, "NoSuchDevice-不存在的设备", "OAxis_01")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := Build(ctx, BuildRequest{
		CLI:           cli,
		WorkspaceRoot: filepath.Dir(specPath),
		SpecPath:      specPath,
		OutPath:       filepath.Join(filepath.Dir(specPath), "out.motion"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK {
		t.Fatal("未声明的设备不应判为通过")
	}
	if result.Validation == nil || !result.Validation.HasErrors {
		t.Fatalf("应带出预检错误: %+v", result.Validation)
	}
	var found bool
	for _, issue := range result.Validation.Issues {
		if strings.Contains(issue.Code, "undeclared") || strings.Contains(issue.Message, "未在设备表中声明") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应指明设备未声明: %+v", result.Validation.Issues)
	}
}

// 轨道映射是最快的自动化断言路径（不建设备、不开窗口）。
func TestIntegrationReadTracksIsFast(t *testing.T) {
	cli := locateOrSkip(t)
	if cli.DeviceTable == "" {
		t.Skip("该 IOStudio 安装没有设备表")
	}
	table, err := ReadDeviceTable(cli.ConfigDir, cli.DeviceTable)
	if err != nil || len(table.Devices) == 0 {
		t.Skip("没有可用设备表")
	}
	table0 := table.Devices[0]
	specPath := buildSpec(t, table0.Name, "OAxis_01")
	dir := filepath.Dir(specPath)
	outPath := filepath.Join(dir, "out.motion")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 先构建出一个真的动作文件（不预检，避免设备表差异干扰本用例）。
	build, err := Build(ctx, BuildRequest{
		CLI: cli, WorkspaceRoot: dir, SpecPath: specPath, OutPath: outPath, SkipValidate: true,
	})
	if err != nil || !build.OK {
		t.Skipf("未能构建出用于取轨道映射的动作文件: %v %+v", err, build)
	}

	started := time.Now()
	tracks, err := ReadTracks(ctx, cli, dir, outPath)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 30*time.Second {
		t.Fatalf("取轨道映射应很快（不建设备、不开窗口），实际 %s", time.Since(started))
	}
	if len(tracks.Tracks) == 0 {
		t.Fatal("应返回轨道映射")
	}
}
