// Command himind-skill-package 把一个技能工程打成 .hmskill。
//
// 实现放在 tooling/commands，官方仓与生态里其它扩展仓共用同一份打包规则。
package main

import "github.com/MrBaoquan/himind-extensions/tooling/commands"

func main() {
	commands.SkillPackageMain()
}
