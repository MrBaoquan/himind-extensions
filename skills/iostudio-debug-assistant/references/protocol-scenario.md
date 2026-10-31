# 协议测试场景格式

协议测试场景是一个 JSON 文件，由 `--protocol-test <场景> --report <输出>` 执行。
本文件记录**已验证可用**的字段与取值，来源是 IOStudio 自带示例（安装目录 `Config/ProtocolTests/*.json`）
与 `--help` 的退出码约定。运行时若报字段错误，以安装目录里的示例为权威——它随产品版本更新。

## 结构

```json
{
  "name": "场景名（中文简述，会出现在报告里）",
  "timeout_ms": 3000,
  "steps": [
    { "op": "listen", "as": "srv", "transport": "tcp-server", "port": 0 },
    { "op": "connect", "as": "cli", "transport": "tcp-client", "host": "127.0.0.1", "port_from": "srv" },
    { "op": "send", "via": "cli", "encoding": "utf8", "data": "HELLO" },
    { "op": "expect", "via": "srv", "match": { "type": "equals", "value": "HELLO" } },
    { "op": "close", "via": "cli" },
    { "op": "close", "via": "srv" }
  ]
}
```

- `timeout_ms`：步骤默认超时，可被单个步骤的 `timeout_ms` 覆盖。
- `steps`：**顺序执行**；某一步失败后，后续步骤不再产生有效结果，以报告里的 `steps` 为准。
- `name` 写清做什么、面向哪个对端，现场看报告时才有用。

## 操作

| `op` | 作用 | 关键字段 |
| --- | --- | --- |
| `listen` | 起一个监听端点，等待对端接入 | `as`（会话名）、`transport`、`port`（`0` = 自动分配）、`frame` |
| `connect` | 作为客户端连一个端点 | `as`、`transport`、`host`、`port_from`（复用某个监听端点实际分配的端口）、`path`（WebSocket） |
| `send` | 发一条报文 | `via`（会话名）、`encoding`（`utf8` / `hex`）、`data` |
| `expect` | 等一条报文并断言 | `via`、`match`、`timeout_ms` |
| `close` | 关闭会话 | `via` |

`as` 定义的会话名，就是后续 `send` / `expect` / `close` 的 `via`。
`port_from` 让客户端连到 `listen` 里 `port: 0` 实际分到的端口——**不要**在自回环场景里写死端口。

## 传输方式

| `transport` | 说明 | 常用字段 |
| --- | --- | --- |
| `tcp-server` | TCP 服务端 | `port` |
| `tcp-client` | TCP 客户端 | `host`、`port` 或 `port_from` |
| `udp` | UDP 端点（可做监听也可做客户端） | `port`、`host`、`port_from` |
| `serial` | 串口 | `device`、`baud` |
| `websocket-server` | WebSocket 服务端 | `port` |
| `websocket-client` | WebSocket 客户端 | `host`、`port_from`、`path` |

串口设备可以用环境变量占位：`"device": "${IOUI_TEST_SERIAL}"`，便于同一份场景在不同机器上跑。

## 组帧（`frame`）

默认按原始字节流处理。TCP 与串口下需要稳定断言时要显式组帧，否则一次读到多少字节是不确定的：

| `mode` | 字段 | 适用 |
| --- | --- | --- |
| `delimiter` | `delimiter`、`delimiter_encoding` | 行协议（如 `\r\n` 结尾的文本协议） |
| `fixed` | `size` | 定长报文 |
| `length-prefix` | `prefix_bytes`、`endian` | 头部长度的二进制协议 |

`frame` 可以写在 `listen` 上，也可以写在 `connect` 上，两端各自组帧。

## 断言（`match`）

| `type` | 语义 | 额外字段 |
| --- | --- | --- |
| `equals` | 整条报文完全相等 | — |
| `contains` | 包含子串 | — |
| `startswith` | 前缀匹配 | — |
| `regex` | 正则匹配 | — |
| `json` | 解析 JSON 后按路径取值比较 | `path`（如 `Cmd`） |
| `hexEquals` | 按十六进制字节比较（配合 `encoding: "hex"` 发送） | — |

**优先用 `json` 的 `path`，不要用 `contains` 匹配整段 JSON 文本。** 对端调整字段顺序或加空格时，
`contains` 会假失败；`json` 只比对语义。`contains` 适合流式文本协议里找一个短标记。

## 编写规则

1. 一个场景只验证一件事。要验证多条命令，拆成多个场景，失败时更好定位。
2. 断言写对端**应该**发的报文，不要写它"可能"发的报文；不确定就先手工抓一条再固化。
3. 结束前显式 `close` 所有会话，避免端口被后续场景占用。
4. 真机侧场景（只 `listen` + `expect`，等外部设备主动发包）超时要给足——设备重连、初始化都要时间。
5. 自回环场景（自己起服务端也自己连）不依赖外部设备，适合验证链路、权限与组帧实现。
6. 不要把生产端口写进公共场景；现场端口用参数或环境变量区分。

## 报告

`iostudio.protocol.test` 回传的结构：

- `ok`：全部步骤是否通过；
- `report.name` / `report.steps[]`：全量步骤，每步含 `index`、`op`、`via`、`ok`、`elapsedMs`、`reason`；
- `report.summary`：`passed` / `failed` 计数；
- `failed_steps`：只含失败步骤（已裁剪，给用户看这个就够）；
- `exit_code`：0 = 全部通过，2 = 有失败步骤；
- `message`：失败时的中文摘要；若提到"未产出报告"，问题在场景文件本身。

报告默认不落盘。需要作为交付证据时才传 `keep_report: true`，并在回复里给出报告路径。
