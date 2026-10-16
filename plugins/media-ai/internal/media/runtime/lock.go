package runtime

import _ "embed"

// 把锁定文件内嵌进二进制。
//
// 为什么内嵌而不是运行时读磁盘：这份文件是**版本契约**，必须与代码同版本发布 ——
// 若从外部读取，用户误改或残留旧副本都会让"锁定的摘要"失去意义。
//
//go:embed lock.json
var lockPayload []byte

// EmbeddedLock 返回内嵌的锁定文件内容。
func EmbeddedLock() []byte { return lockPayload }
