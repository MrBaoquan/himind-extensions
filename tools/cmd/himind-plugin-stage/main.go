// Command himind-plugin-stage 把一个已构建的插件目录摊平成打包用暂存目录。
//
// 实现放在 tooling/commands，官方仓与生态里其它扩展仓共用同一份「什么进包」规则。
package main

import "github.com/MrBaoquan/himind-extensions/tooling/commands"

func main() {
	commands.PluginStageMain()
}
