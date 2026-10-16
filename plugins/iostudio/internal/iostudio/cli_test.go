package iostudio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这份设备表照抄真实结构（含 Properties/Key 的 SlewRate 与没有 Key 的动作）。
const sampleTable = `<?xml version="1.0" encoding="utf-8"?>
<IORoot>
  <Device Name="YDL-0" Type="External" DllName="MP-YDL" Index="0">
    <Properties>
      <Key Name="OAxis_06" SlewRate="0.5" />
    </Properties>
    <OAction Name="Heave" Label="电缸升降">
      <Key Name="OAxis_06" />
    </OAction>
    <OAction Name="Water" Label="喷水">
      <Key Name="OAxis_16" />
    </OAction>
    <OAction Name="Broken" Label="缺 Key 的动作" />
  </Device>
</IORoot>
`

func writeTable(t *testing.T, payload string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "IODevice.xml"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, "IODevice.xml"
}

func TestReadDeviceTableParsesActionsAndKeys(t *testing.T) {
	configDir, fileName := writeTable(t, sampleTable)
	table, err := ReadDeviceTable(configDir, fileName)
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Devices) != 1 {
		t.Fatalf("应解析出 1 台设备: %+v", table.Devices)
	}
	device := table.Devices[0]
	if device.Name != "YDL-0" || device.DllName != "MP-YDL" {
		t.Fatalf("设备属性解析错误: %+v", device)
	}
	if len(device.OActions) != 3 {
		t.Fatalf("应解析出 3 个动作: %+v", device.OActions)
	}
	if device.OActions[0].Name != "Heave" || device.OActions[0].Keys[0] != "OAxis_06" {
		t.Fatalf("动作解析错误: %+v", device.OActions[0])
	}
	if device.OActions[0].Label != "电缸升降" {
		t.Fatalf("标签解析错误: %+v", device.OActions[0])
	}
	if len(device.Properties) != 1 || device.Properties[0].SlewRate != 0.5 {
		t.Fatalf("Key 属性解析错误: %+v", device.Properties)
	}
}

// 没有 Key 的动作下发时不会落到任何通道——这种"配了却无效"必须被指出来。
func TestReadDeviceTableFlagsActionWithoutKeys(t *testing.T) {
	configDir, fileName := writeTable(t, sampleTable)
	table, err := ReadDeviceTable(configDir, fileName)
	if err != nil {
		t.Fatal(err)
	}
	var broken *Action
	for index := range table.Devices[0].OActions {
		if table.Devices[0].OActions[index].Name == "Broken" {
			broken = &table.Devices[0].OActions[index]
		}
	}
	if broken == nil {
		t.Fatal("未找到 Broken 动作")
	}
	if broken.Usable {
		t.Fatalf("没有 Key 的动作应标记为不可用: %+v", broken)
	}
	if len(table.Warnings) == 0 {
		t.Fatal("应对没有 Key 的动作给出告警")
	}
	found := false
	for _, warning := range table.Warnings {
		if strings.Contains(warning, "Broken") {
			found = true
		}
	}
	if !found {
		t.Fatalf("告警应点名具体动作: %v", table.Warnings)
	}
}

// 设备表是人工维护的，坏 XML 时有发生；应报"表坏了"而不是含糊地"没有设备"。
//
// 注意用真正畸形的 XML（未闭合）：Go 的解析器容忍重复的 <?xml?> 声明，
// 而 .NET 不容忍 —— 也就是说"IOStudio 读不了的表，本插件可能读得下去"。
// 这正是插件不能替 IOStudio 下"通过"结论的原因之一。
func TestReadDeviceTableReportsBrokenXML(t *testing.T) {
	configDir, fileName := writeTable(t, "<IORoot><Device Name=\"A\"></IORoot>")
	table, err := ReadDeviceTable(configDir, fileName)
	if err != nil {
		t.Fatalf("坏 XML 不应直接返回错误，而应带告警返回: %v", err)
	}
	if len(table.Warnings) == 0 {
		t.Fatal("坏 XML 应产生告警")
	}
	if !strings.Contains(table.Warnings[0], "XML") {
		t.Fatalf("告警应指明是 XML 解析问题: %v", table.Warnings)
	}
}

func TestReadDeviceTableMissingFile(t *testing.T) {
	if _, err := ReadDeviceTable(t.TempDir(), "nope.xml"); err == nil {
		t.Fatal("文件不存在时应报错")
	}
}

func TestReadDeviceTableEmptyHasWarning(t *testing.T) {
	configDir, fileName := writeTable(t, `<?xml version="1.0"?><IORoot></IORoot>`)
	table, err := ReadDeviceTable(configDir, fileName)
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Warnings) == 0 {
		t.Fatal("没有设备时应给出告警")
	}
}

// 状态文件是程序间的结构化契约，必须完整读出来。
func TestReadStatusParsesValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	payload := `{
	  "phase": "validated",
	  "motion": "a.motion",
	  "motionExists": true,
	  "validation": {
	    "hasErrors": true,
	    "errorCount": 1,
	    "warningCount": 2,
	    "infoCount": 0,
	    "issues": [
	      {"code": "device.undeclared", "severity": "error", "message": "设备未声明",
	       "trackId": "trk_1", "timeMs": 1200}
	    ]
	  }
	}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := ReadStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "validated" || status.Validation == nil {
		t.Fatalf("状态解析不完整: %+v", status)
	}
	if !status.Validation.HasErrors || status.Validation.ErrorCount != 1 {
		t.Fatalf("预检结果解析错误: %+v", status.Validation)
	}
	issue := status.Validation.Issues[0]
	if issue.Code != "device.undeclared" {
		t.Fatalf("问题码解析错误: %+v", issue)
	}
	// 版本新增的字段不能被丢掉，否则调用方拿不到定位信息。
	if issue.Extra["trackId"] != "trk_1" {
		t.Fatalf("未知字段应保留在 Extra: %+v", issue.Extra)
	}
}

func TestReadStatusMissingFileIsNotAnError(t *testing.T) {
	status, err := ReadStatus(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("状态文件不存在不应算错误: %v", err)
	}
	if status != nil {
		t.Fatalf("应为 nil: %+v", status)
	}
}

func TestIssueRoundTripPreservesExtraFields(t *testing.T) {
	payload := []byte(`{"code":"c","severity":"warning","message":"m","lane":"安全","index":3}`)
	var issue Issue
	if err := issue.UnmarshalJSON(payload); err != nil {
		t.Fatal(err)
	}
	encoded, err := issue.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{`"code":"c"`, `"severity":"warning"`, `"message":"m"`, `"lane":"安全"`, `"index":3`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("往返后丢失 %s: %s", expected, text)
		}
	}
}

func TestResolveConfigDirHandlesBinShim(t *testing.T) {
	// bin\iostudio.cmd 的上层才是应用根。
	if got := resolveConfigDir(filepath.Join("C:", "app", "bin", "iostudio.cmd")); got != filepath.Join("C:", "app", "Config") {
		t.Fatalf("shim 场景解析错误: %s", got)
	}
	if got := resolveConfigDir(filepath.Join("C:", "app", "IOStudio.exe")); got != filepath.Join("C:", "app", "Config") {
		t.Fatalf("exe 场景解析错误: %s", got)
	}
}

func TestConfiguredDeviceTableReadsSetting(t *testing.T) {
	dir := t.TempDir()
	settings := `<?xml version="1.0" encoding="utf-8"?>
<IOStudioSettings UIRefreshIntervalMs="100" DeviceTableFile="IODevice.YDL.xml" />`
	if err := os.WriteFile(filepath.Join(dir, "IOStudioSettings.xml"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := configuredDeviceTable(dir); got != "IODevice.YDL.xml" {
		t.Fatalf("未读出配置的设备表: %q", got)
	}
	if got := configuredDeviceTable(t.TempDir()); got != "" {
		t.Fatalf("没有设置文件时应返回空: %q", got)
	}
}

func TestIsScriptDetectsBatchFiles(t *testing.T) {
	if !isScript("C:/x/iostudio.cmd") || !isScript("C:/x/run.BAT") {
		t.Fatal("应识别 .cmd/.bat")
	}
	if isScript("C:/x/IOStudio.exe") {
		t.Fatal(".exe 不应被当成脚本")
	}
}
