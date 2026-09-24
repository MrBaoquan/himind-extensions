package distribution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReleaseManifestSchema 是发布清单的版本标识。仓库脚本、Agent 内置发布器与
// 安装器共用同一个值，改结构必须同时改这里。
const ReleaseManifestSchema = "himind_extension_release.v1"

// ReleaseArtifact 是清单里对制品的精确描述。
type ReleaseArtifact struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// ReleaseSignature 是制品分离签名。清单里有该字段就代表这次发布是签名的，
// 消费侧必须验签通过；字段缺失表示未签名发布，是否接受由分发策略决定。
type ReleaseSignature struct {
	FileName           string `json:"file_name"`
	FileSize           int64  `json:"file_size"`
	SHA256             string `json:"sha256"`
	Signature          string `json:"signature"`
	SignatureKeyID     string `json:"signature_key_id"`
	SignatureAlgorithm string `json:"signature_algorithm"`
}

// ReleaseManifest 是一次发布留在 Release 上的完整记录。
//
// 它只承载「装这个版本需要什么」：制品名、摘要、依赖精确 pin、签名。
// 面向市场的展示信息（名称、说明、分类）属于源码清单，不在这里重复，
// 避免同一份事实出现两个可各自漂移的副本。
type ReleaseManifest struct {
	SchemaVersion   string              `json:"schema_version"`
	Repository      string              `json:"repository"`
	Tag             string              `json:"tag"`
	Kind            string              `json:"kind"`
	ID              string              `json:"id"`
	Version         string              `json:"version"`
	Channel         string              `json:"channel"`
	SourceCommit    string              `json:"source_commit"`
	MinAgentVersion string              `json:"min_agent_version"`
	Artifact        ReleaseArtifact     `json:"artifact"`
	Dependencies    []ReleaseDependency `json:"dependencies,omitempty"`
	Signature       *ReleaseSignature   `json:"signature,omitempty"`
}

// ReleaseDependencySource 定位一个依赖制品：仓库 + tag + 制品地址。
// 字段与 Agent 侧的安装计划一一对应，消费侧不猜来源。
type ReleaseDependencySource struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Repository  string `json:"repository"`
	Reference   string `json:"reference"`
	ArtifactURL string `json:"artifact_url"`
}

// ReleaseDependency 是发布清单里的一条依赖 pin。
//
// pinned 为 false 表示只知道最低版本、无法定位到某个确定制品（例如依赖还没
// 进市场索引）。必需依赖不允许停在这个状态，Validate 会拦下。
type ReleaseDependency struct {
	Kind       string                  `json:"kind"`
	ID         string                  `json:"id"`
	Required   bool                    `json:"required"`
	MinVersion string                  `json:"min_version"`
	Version    string                  `json:"version"`
	SHA256     string                  `json:"sha256"`
	Source     ReleaseDependencySource `json:"source"`
	Pinned     bool                    `json:"pinned"`
}

// Validate 校验清单自洽：版本标识、规范 tag、规范制品名三者必须指向同一个版本。
// 消费侧与门禁都用它，避免「清单能解析但装不上」。
func (m ReleaseManifest) Validate() error {
	if m.SchemaVersion != ReleaseManifestSchema {
		return fmt.Errorf("发布清单版本不受支持: %s（需要 %s）", m.SchemaVersion, ReleaseManifestSchema)
	}
	kind, id, version, err := ParseReleaseTag(m.Tag)
	if err != nil {
		return err
	}
	if kind != strings.TrimSpace(m.Kind) || id != strings.TrimSpace(m.ID) || version != strings.TrimSpace(m.Version) {
		return fmt.Errorf("发布清单 tag 与 kind/id/version 不一致: %s", m.Tag)
	}
	expected, err := ArtifactName(kind, id, version)
	if err != nil {
		return err
	}
	if strings.TrimSpace(m.Artifact.Name) != expected {
		return fmt.Errorf("制品名 %q 不是规范形式，应为 %q", m.Artifact.Name, expected)
	}
	if err := ValidateSHA256("artifact sha256", m.Artifact.SHA256); err != nil {
		return err
	}
	if m.Signature != nil {
		if m.Signature.Signature == "" || m.Signature.SignatureKeyID == "" {
			return errors.New("发布清单声明了签名但内容不完整")
		}
		if m.Signature.SignatureAlgorithm != "rsa-pss-sha256" {
			return fmt.Errorf("签名算法不受支持: %s", m.Signature.SignatureAlgorithm)
		}
		if err := ValidateSHA256("signature sha256", m.Signature.SHA256); err != nil {
			return err
		}
	}
	for _, dependency := range m.Dependencies {
		if err := dependency.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate 校验一条依赖 pin 自洽：类型受支持、ID 非空、必需依赖必须可定位。
func (d ReleaseDependency) Validate() error {
	if !KnownKind(strings.TrimSpace(d.Kind)) {
		return fmt.Errorf("依赖类型不受支持: %q", d.Kind)
	}
	if strings.TrimSpace(d.ID) == "" {
		return errors.New("依赖缺少 id")
	}
	if strings.TrimSpace(d.Version) == "" {
		if d.Required {
			return fmt.Errorf("必需依赖 %s 未解析到确定版本，不能发布", d.ID)
		}
		return nil
	}
	if d.Source.Reference != "" || d.Source.Repository != "" {
		if d.Source.Kind == "" || strings.TrimSpace(d.Source.Repository) == "" || strings.TrimSpace(d.Source.Reference) == "" {
			return fmt.Errorf("依赖 %s 的来源不完整", d.ID)
		}
	}
	if strings.TrimSpace(d.SHA256) != "" {
		if err := ValidateSHA256("dependency sha256", d.SHA256); err != nil {
			return err
		}
	}
	return nil
}

// DownloadURLOf 返回该版本制品的下载地址。
func (m ReleaseManifest) DownloadURLOf(repository string) string {
	return DownloadURL(repository, m.Tag, m.Artifact.Name)
}

// DependencyList 返回依赖列表；缺省是空数组，保证清单自描述。
func (m ReleaseManifest) DependencyList() []ReleaseDependency {
	if m.Dependencies == nil {
		return []ReleaseDependency{}
	}
	return m.Dependencies
}

// ReadReleaseManifest 读取发布清单。
func ReadReleaseManifest(path string) (ReleaseManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ReleaseManifest{}, err
	}
	var value ReleaseManifest
	if err := json.Unmarshal(data, &value); err != nil {
		return ReleaseManifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return value, nil
}

// WriteReleaseManifest 写发布清单。同一份内容总是得到同样的字节，
// 让「同一版本重复发布」可以直接比对文件而不是靠时间戳。
func WriteReleaseManifest(path string, value ReleaseManifest) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// SignatureOf 返回发布清单里的内嵌签名。
func (m ReleaseManifest) SignatureOf() (ReleaseSignature, error) {
	if m.Signature == nil {
		return ReleaseSignature{}, errors.New("发布清单没有签名")
	}
	return *m.Signature, nil
}

// SignatureFromArtifact 用本地制品与签名文件构造签名元数据，并在使用前校验一致。
func SignatureFromArtifact(artifactPath, signaturePath string) (ReleaseSignature, error) {
	data, err := os.ReadFile(signaturePath)
	if err != nil {
		return ReleaseSignature{}, err
	}
	var signed ReleaseSignature
	if err := json.Unmarshal(data, &signed); err != nil {
		return ReleaseSignature{}, fmt.Errorf("%s: %w", signaturePath, err)
	}
	if err := VerifySignedArtifact(artifactPath, signed); err != nil {
		return ReleaseSignature{}, err
	}
	return signed, nil
}

// VerifySignedArtifact 校验签名元数据与本地制品逐字节一致。
func VerifySignedArtifact(artifactPath string, signed ReleaseSignature) error {
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if signed.FileName != filepath.Base(artifactPath) || signed.FileSize != int64(len(data)) ||
		!strings.EqualFold(signed.SHA256, hex.EncodeToString(digest[:])) {
		return errors.New("签名元数据与本地制品不一致")
	}
	if signed.Signature == "" || signed.SignatureKeyID == "" || signed.SignatureAlgorithm != "rsa-pss-sha256" {
		return errors.New("签名元数据不完整")
	}
	return nil
}

// ValidateSHA256 校验摘要字段是 64 位十六进制。
func ValidateSHA256(field, value string) error {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != 64 {
		return fmt.Errorf("%s 必须是 64 位十六进制: %q", field, value)
	}
	if _, err := hex.DecodeString(trimmed); err != nil {
		return fmt.Errorf("%s 必须是 64 位十六进制: %q", field, value)
	}
	return nil
}
