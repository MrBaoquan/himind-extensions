package commands

import "strings"

// stringListFlag 是「可重复指定」的字符串命令行参数，重复几次就累积几项。
//
// release-plan 用它接收多个依赖仓索引：参数顺序即合并顺序，读取时不排序，
// 保证同一组参数总是得到同一份 pin 结果。
type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		*f = append(*f, value)
	}
	return nil
}
