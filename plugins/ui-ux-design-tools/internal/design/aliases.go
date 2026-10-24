package design

import (
	"sort"
	"strings"
)

var chineseAliases = map[string]string{
	"仪表盘": "dashboard analytics", "后台": "admin dashboard", "看板": "dashboard",
	"工业": "industrial engineering", "工业设备": "industrial equipment iot device management",
	"工业控制": "industrial control hmi", "设备": "device equipment", "设备管理": "device management iot",
	"调试": "developer tool ide debugging", "工作台": "desktop developer tool workstation dashboard",
	"控制台": "console developer tool dashboard", "专业软件": "professional desktop software",
	"高信息密度": "data dense dashboard", "信息密度": "data dense",
	"实时数据": "real-time monitoring data", "数据采集": "data acquisition monitoring telemetry",
	"物联网": "iot device management telemetry", "硬件": "hardware embedded device",
	"企业": "enterprise b2b", "数据密集": "data dense", "数据密集型": "data dense",
	"金融科技": "fintech banking", "银行": "banking finance", "保险": "insurance",
	"医疗": "healthcare medical", "健康": "health wellness", "教育": "education learning",
	"电商": "ecommerce checkout shopping", "零售": "retail commerce",
	"游戏": "gaming entertainment", "社交": "social community", "协作": "collaboration team",
	"落地页": "landing page", "首屏": "hero", "营销": "marketing",
	"移动端": "mobile", "桌面端": "desktop", "响应式": "responsive",
	"无障碍": "accessibility a11y", "键盘": "keyboard focus", "焦点": "focus states",
	"深色": "dark mode", "浅色": "light mode", "主题": "theme",
	"极简": "minimalism", "玻璃": "glassmorphism", "新拟态": "neumorphism",
	"粗野": "brutalism", "便当": "bento grid", "复古": "retro", "赛博": "cyberpunk",
	"奢华": "luxury premium", "儿童": "children kids", "专业": "professional",
	"实时": "real-time", "监控": "monitoring", "图表": "chart visualization",
	"字体": "typography font", "排版": "typography", "配色": "color palette",
	"中文": "cjk chinese fonts", "预约": "booking appointment", "结账": "checkout payment",
	"登录": "login authentication", "设置": "settings profile", "搜索": "search results",
	"空状态": "empty state", "表格": "table data grid", "性能": "performance",
	"动画": "animation motion", "交互": "interaction", "错误": "error validation",
	"表单": "form validation", "导航": "navigation", "图标": "icons",
	"表单验证": "form validation error", "数据表格": "data table grid", "状态反馈": "state feedback loading empty error",
	"空数据": "empty state zero data", "错误提示": "error message validation", "暗色模式": "dark mode",
	"品牌色": "brand color primary", "组件规范": "component specification", "可访问性": "accessibility a11y",
	"响应式布局": "responsive layout", "设计系统": "design system tokens", "中文字体": "cjk chinese font",
}

func ExpandQuery(query string) string {
	parts := []string{strings.TrimSpace(query)}
	keys := make([]string, 0, len(chineseAliases))
	for key := range chineseAliases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, zh := range keys {
		if strings.Contains(query, zh) {
			parts = append(parts, chineseAliases[zh])
		}
	}
	return strings.Join(parts, " ")
}
