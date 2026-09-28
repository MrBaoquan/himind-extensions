// Command himind-catalog-upsert 把一次发布写进市场索引。
//
// 实现放在 tooling/commands，官方仓与生态里其它扩展仓共用同一份索引规则。
package main

import "github.com/MrBaoquan/himind-extensions/tooling/commands"

func main() {
	commands.CatalogUpsertMain()
}
