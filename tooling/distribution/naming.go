package distribution

import (
	"fmt"
	"net/url"
	"strings"
)

// 扩展制品的命名规范。发布方（仓库脚本、Agent 内置发布器）与消费方
// （目录清单、按 Release 安装）必须解析同一份规则，否则会出现「发了但装不上」。
//
//	tag      <kind>/<id>@<version>      例如 plugin/com.himind.tech-radar@0.4.2
//	制品     <id>-<version>.<ext>       例如 com.himind.tech-radar-0.4.2.hmpkg
//	清单     <id>@<version>.json
//
// 签名不单独成资产：RSA-PSS/SHA-256 的 signature、signature_key_id 与
// signature_algorithm 内嵌在发布清单里，与制品摘要同一次写入，避免
// 「制品和签名文件走散」这种无法自证的组合。
//
// 选择 `<kind>/<id>@<version>` 而不是扁平前缀的原因：聚合仓库里 tag 列表按 kind
// 分组可读；`@version` 与精确 pin、溯源一一对应；与 changesets 生态的通行做法一致。

const (
	// KindPlugin、KindSkill、KindWorkflow 是三类扩展制品。
	KindPlugin   = "plugin"
	KindSkill    = "skill"
	KindWorkflow = "workflow"
)

// Kinds 返回全部扩展类型，顺序即规范化顺序。
func Kinds() []string {
	return []string{KindPlugin, KindSkill, KindWorkflow}
}

// KnownKind 报告类型是否为受支持的扩展类型。
func KnownKind(kind string) bool {
	for _, item := range Kinds() {
		if item == kind {
			return true
		}
	}
	return false
}

// ArtifactExtension 返回该类型制品的文件扩展名（不含点）。
func ArtifactExtension(kind string) (string, error) {
	switch kind {
	case KindPlugin:
		return "hmpkg", nil
	case KindSkill:
		return "hmskill", nil
	case KindWorkflow:
		return "hmwf", nil
	default:
		return "", fmt.Errorf("不支持的扩展类型 %q", kind)
	}
}

// ReleaseTag 返回扩展版本的 GitHub Release tag：`<kind>/<id>@<version>`。
func ReleaseTag(kind, id, version string) (string, error) {
	if err := ensureTagPart("kind", kind); err != nil {
		return "", err
	}
	if !KnownKind(strings.TrimSpace(kind)) {
		return "", fmt.Errorf("不支持的扩展类型 %q", kind)
	}
	if err := ensureTagPart("id", id); err != nil {
		return "", err
	}
	if err := ensureTagPart("version", version); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s@%s", strings.TrimSpace(kind), strings.TrimSpace(id), strings.TrimSpace(version)), nil
}

// ParseReleaseTag 解析规范 tag，返回类型、ID 与版本。
// 非规范 tag（例如历史的 `plugin-<id>-v<version>`）一律拒绝，便于把漂移暴露在门禁里。
func ParseReleaseTag(tag string) (kind, id, version string, err error) {
	value := strings.TrimSpace(tag)
	slash := strings.Index(value, "/")
	if slash <= 0 {
		return "", "", "", fmt.Errorf("tag %q 不是 <kind>/<id>@<version> 形式", tag)
	}
	kind = value[:slash]
	rest := value[slash+1:]
	at := strings.LastIndex(rest, "@")
	if at <= 0 || at == len(rest)-1 {
		return "", "", "", fmt.Errorf("tag %q 不是 <kind>/<id>@<version> 形式", tag)
	}
	id, version = rest[:at], rest[at+1:]
	expected, err := ReleaseTag(kind, id, version)
	if err != nil {
		return "", "", "", err
	}
	if expected != value {
		return "", "", "", fmt.Errorf("tag %q 不是规范形式，应为 %q", tag, expected)
	}
	return kind, id, version, nil
}

// ArtifactName 返回制品文件名：`<id>-<version>.<ext>`。
func ArtifactName(kind, id, version string) (string, error) {
	extension, err := ArtifactExtension(kind)
	if err != nil {
		return "", err
	}
	if err := ensureTagPart("id", id); err != nil {
		return "", err
	}
	if err := ensureTagPart("version", version); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s.%s", strings.TrimSpace(id), strings.TrimSpace(version), extension), nil
}

// ManifestName 返回发布清单文件名：`<id>@<version>.json`。
func ManifestName(id, version string) string {
	return fmt.Sprintf("%s@%s.json", strings.TrimSpace(id), strings.TrimSpace(version))
}

// BatchTagPrefix 是批次 Release 的 tag 前缀。
//
// 一个仓库里的扩展可以多到几百件（ECC 技能库就是 268 件技能）。每件单独占一条
// Release 时，Release 列表会退化成版本流水账：翻不到「当前这一批是什么」，回收
// 也只能逐条做，而逐条回收必然落后于逐条发布。
//
// 批次把「一次同步」收敛成一条 Release：tag 里只放这一批的版本号，成员身份仍然
// 写在各自的制品名与发布清单里，命名规则一点没变。批次 tag 不带 `@`，与
// `<kind>/<id>@<version>` 不可能撞名。
const BatchTagPrefix = "batch/"

// BatchTag 返回批次 Release 的 tag：`batch/<version>`。
func BatchTag(version string) (string, error) {
	if err := ensureTagPart("version", version); err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(version)
	// 批次 tag 只有两段。放行斜杠会让 `batch/batch/2.2.3` 也解析成合法版本，
	// 「前缀就能认出归属」这条判断随之失效。
	if strings.Contains(trimmed, "/") {
		return "", fmt.Errorf("批次版本不能含 /: %q", version)
	}
	return BatchTagPrefix + trimmed, nil
}

// ParseBatchTag 解析批次 tag，返回这一批的版本号。
// 非批次 tag 一律拒绝，调用方据此把「扩展自己的 tag」与「批次 tag」分流。
func ParseBatchTag(tag string) (string, error) {
	value := strings.TrimSpace(tag)
	if !strings.HasPrefix(value, BatchTagPrefix) {
		return "", fmt.Errorf("tag %q 不是 %s<version> 形式的批次 tag", tag, BatchTagPrefix)
	}
	version := strings.TrimPrefix(value, BatchTagPrefix)
	expected, err := BatchTag(version)
	if err != nil {
		return "", err
	}
	if expected != value {
		return "", fmt.Errorf("tag %q 不是规范形式，应为 %q", tag, expected)
	}
	return version, nil
}

// IsBatchTag 报告 tag 是否为批次 tag。
func IsBatchTag(tag string) bool {
	_, err := ParseBatchTag(tag)
	return err == nil
}

// BatchManifestName 返回批次清单文件名：`batch@<version>.json`。
// 批次清单记录这一批覆盖了什么，制品与发布清单本身不带批次信息。
func BatchManifestName(version string) string {
	return fmt.Sprintf("batch@%s.json", strings.TrimSpace(version))
}

// DownloadURL 返回制品在 GitHub Release 上的下载地址。
func DownloadURL(repository, tag, fileName string) string {
	return fmt.Sprintf("%s/releases/download/%s/%s", ReleaseBaseURL(repository), url.PathEscape(tag), url.PathEscape(fileName))
}

// ReleaseBaseURL 返回仓库的 Release 根地址。
func ReleaseBaseURL(repository string) string {
	return fmt.Sprintf("https://github.com/%s", strings.TrimSpace(repository))
}

// ensureTagPart 拦截会破坏 tag 或文件名的字符，避免把非法名字推到远端才失败。
func ensureTagPart(field, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s 不能为空", field)
	}
	if trimmed != value {
		return fmt.Errorf("%s 前后不能有空白: %q", field, value)
	}
	if strings.ContainsAny(trimmed, " ~^:?*[\\@") || strings.Contains(trimmed, "..") ||
		strings.Contains(trimmed, "//") ||
		strings.HasPrefix(trimmed, "/") || strings.HasSuffix(trimmed, "/") {
		return fmt.Errorf("%s 含非法字符: %q", field, value)
	}
	return nil
}
