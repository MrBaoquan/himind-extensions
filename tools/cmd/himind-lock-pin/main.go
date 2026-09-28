// Command himind-lock-pin 把工作流扩展锁里的依赖摘要改写为依赖制品的载荷摘要。
//
// 实现放在 tooling/commands，依赖解析与发布清单共用一份规则。
package main

import "github.com/MrBaoquan/himind-extensions/tooling/commands"

func main() {
	commands.LockPinMain()
}
