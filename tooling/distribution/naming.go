package distribution

import (
	"fmt"
	"net/url"
	"strings"
)

// 扩展制品的命名规范。发布方（仓库脚本、Agent 内置发布器）与消费方
// （目录清单、按 Release 安装）必须解析同一份规则，否则会出现「发了但装不上」。
//
//	tag      <kind>/<id>@<version>      例如 plugin/com.himind.image-optimizer@1.0.2
//	制品     <id>-<version>.<ext>       例如 com.himind.image-optimizer-1.0.2.hmpkg
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
