// Command himind-repo-check 校验当前目录作为一个扩展仓是否自洽。
//
// 实现只有一份，放在 tooling/commands：官方仓与生态里其它扩展仓（例如项目定制
// 交付仓）共用同一份校验规则，新仓不会因为复制脚本而与官方仓漂移。
package main

import (
	"fmt"
	"os"

	"github.com/MrBaoquan/himind-extensions/tooling/commands"
)

func main() {
	if err := commands.RepoCheckMain(); err != nil {
		fmt.Fprintln(os.Stderr, "extension repository is invalid:", err)
		os.Exit(1)
	}
}
