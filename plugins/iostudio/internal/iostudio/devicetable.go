package iostudio

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 设备表读取。
//
// 为什么要读它：AI 要写出一份能通过预检的演出规格，就必须先知道"这台设备上有哪些动作、
// 各自落到哪个通道"。否则只能靠猜通道名，而猜错的表现是**静默丢弃输出**
// （native 侧查不到设备的通道会直接丢掉，不报错、只是设备不动）。
//
// 只读不写：设备表属于现场配置，插件不该改它。

// DeviceTable 一份设备表的内容摘要。
type DeviceTable struct {
	File    string   `json:"file"`
	Path    string   `json:"path"`
	Devices []Device `json:"devices"`
	// Warnings 表本身的问题（如 XML 损坏、OAction 没有 Key）。
	Warnings []string `json:"warnings,omitempty"`
}

// Device 一台设备。
type Device struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	DllName  string `json:"dll_name,omitempty"`
	Index    string `json:"index,omitempty"`
	OActions []Action `json:"oactions"`
	// Properties 里带速率上限等全局 Key 属性 —— 限速配错会让动作变慢却毫无提示。
	Properties []PropertyKey `json:"properties,omitempty"`
}

// Action 一个可下发动作。
type Action struct {
	Name   string   `json:"name"`
	Label  string   `json:"label,omitempty"`
	Keys   []string `json:"keys"`
	// Usable 为 false 表示该动作没有 Key，下发时不会被解析成任何通道。
	Usable bool `json:"usable"`
}

// PropertyKey 全局 Key 属性。
type PropertyKey struct {
	Name     string  `json:"name"`
	SlewRate float64 `json:"slew_rate,omitempty"`
}

type rawRoot struct {
	Devices []rawDevice `xml:"Device"`
}

type rawDevice struct {
	Name       string       `xml:"Name,attr"`
	Type       string       `xml:"Type,attr"`
	DllName    string       `xml:"DllName,attr"`
	Index      string       `xml:"Index,attr"`
	Properties *rawProps    `xml:"Properties"`
	OActions   []rawOAction `xml:"OAction"`
}

type rawProps struct {
	Keys []rawKey `xml:"Key"`
}

type rawOAction struct {
	Name  string   `xml:"Name,attr"`
	Label string   `xml:"Label,attr"`
	Keys  []rawKey `xml:"Key"`
}

type rawKey struct {
	Name     string `xml:"Name,attr"`
	SlewRate string `xml:"SlewRate,attr"`
}

// ReadDeviceTable 读取设备表。
func ReadDeviceTable(configDir, fileName string) (DeviceTable, error) {
	table := DeviceTable{File: fileName}
	if strings.TrimSpace(configDir) == "" || strings.TrimSpace(fileName) == "" {
		return table, fmt.Errorf("未确定设备表位置（config_dir 或设备表文件名为空）")
	}
	table.Path = filepath.Join(configDir, fileName)
	payload, err := os.ReadFile(table.Path)
	if err != nil {
		return table, fmt.Errorf("读取设备表失败: %w", err)
	}

	var root rawRoot
	if err := xml.Unmarshal(payload, &root); err != nil {
		// 设备表是人工维护的，坏 XML 并不罕见（历史上出现过重复的 <?xml?> 声明）。
		// 明确指出而不是含糊报错，否则现场只会看到"设备一个都没有"。
		table.Warnings = append(table.Warnings,
			"设备表 XML 解析失败（文件可能损坏）: "+err.Error())
		return table, nil
	}

	for _, raw := range root.Devices {
		device := Device{
			Name:    raw.Name,
			Type:    raw.Type,
			DllName: raw.DllName,
			Index:   raw.Index,
		}
		if raw.Properties != nil {
			for _, key := range raw.Properties.Keys {
				device.Properties = append(device.Properties, PropertyKey{
					Name:     key.Name,
					SlewRate: parseFloatOrZero(key.SlewRate),
				})
			}
		}
		for _, rawAction := range raw.OActions {
			action := Action{Name: rawAction.Name, Label: rawAction.Label}
			for _, key := range rawAction.Keys {
				action.Keys = append(action.Keys, key.Name)
			}
			action.Usable = len(action.Keys) > 0
			if !action.Usable {
				table.Warnings = append(table.Warnings, fmt.Sprintf(
					"设备 %s 的动作 %s 没有 Key，下发时不会落到任何通道（该动作会被静默忽略）",
					raw.Name, rawAction.Name))
			}
			device.OActions = append(device.OActions, action)
		}
		table.Devices = append(table.Devices, device)
	}

	if len(table.Devices) == 0 {
		table.Warnings = append(table.Warnings,
			"设备表里没有任何设备，动作无法落到硬件上")
	}
	return table, nil
}

func parseFloatOrZero(value string) float64 {
	var parsed float64
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%g", &parsed); err != nil {
		return 0
	}
	return parsed
}
