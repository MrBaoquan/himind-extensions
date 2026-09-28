// Command himind-release-plan 打印一次发布的命名事实，并可按同一份事实写发布清单。
//
// 实现放在 tooling/commands，官方仓与生态里其它扩展仓共用同一份命名规则。
package main

import "github.com/MrBaoquan/himind-extensions/tooling/commands"

func main() {
	commands.ReleasePlanMain()
}
