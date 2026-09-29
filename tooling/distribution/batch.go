package distribution

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// BatchManifestSchema 是批次清单的版本标识。改结构必须同时改这里，
// 门禁与消费侧都按它判断自己读不读得懂。
const BatchManifestSchema = "himind_extension_batch.v1"

// BatchUpstream 记录这批内容搬自上游哪个版本、哪个提交。
//
// 批次 tag 只带本仓库的批次版本，上游版本不参与命名：同一上游版本会被同步很多次
// （上游按自己的节奏发版，同步按周跑），拿它当 tag 必然撞名。溯源信息因此落在
// 清单里，看一眼就知道这批对应上游的什么状态。
type BatchUpstream struct {
	Repository    string `json:"repository"`
	Package       string `json:"package,omitempty"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	CommitDate    string `json:"commit_date,omitempty"`
	License       string `json:"license,omitempty"`
	LicenseHolder string `json:"license_holder,omitempty"`
}

// BatchMember 是批次里的一个成员：身份、版本，以及同一条 Release 上它的制品与发布清单。
type BatchMember struct {
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Version  string          `json:"version"`
	Manifest string          `json:"manifest"`
	Artifact ReleaseArtifact `json:"artifact"`
}

// BatchManifest 是批次 Release 上的总清单，这是这条 Release 的自述。
//
// 一条 Release 装一整批：成员的制品与发布清单都在同一条 Release 里，所以任何一批
// 都能独立回答「我是谁、装了哪些扩展、各自的摘要是什么」。目录索引把每个成员指向
// 批次 tag，装谁都不需要再去找别的 Release。
type BatchManifest struct {
	SchemaVersion string        `json:"schema_version"`
	Repository    string        `json:"repository"`
	Tag           string        `json:"tag"`
	Version       string        `json:"version"`
	Channel       string        `json:"channel"`
	SourceCommit  string        `json:"source_commit"`
	GeneratedAt   string        `json:"generated_at"`
	Upstream      BatchUpstream `json:"upstream"`
	Members       []BatchMember `json:"members"`
}

// Validate 校验批次清单自洽：tag 与版本一致，成员身份、制品名、发布清单名三者同源。
//
// 这里拦的是「清单能解析但装不上」：成员身份与制品名对不上，安装时就找不到资产；
// 同一 (kind,id) 出现两次，则「这个成员是哪个版本」有两个答案。
func (m BatchManifest) Validate() error {
	if m.SchemaVersion != BatchManifestSchema {
		return fmt.Errorf("批次清单版本不受支持: %s（需要 %s）", m.SchemaVersion, BatchManifestSchema)
	}
	version, err := ParseBatchTag(m.Tag)
	if err != nil {
		return err
	}
	if version != strings.TrimSpace(m.Version) {
		return fmt.Errorf("批次清单 tag 与 version 不一致: %s", m.Tag)
	}
	if strings.TrimSpace(m.Repository) == "" {
		return fmt.Errorf("批次清单缺少 repository: %s", m.Tag)
	}
	if strings.TrimSpace(m.SourceCommit) == "" {
		return fmt.Errorf("批次清单缺少 source_commit: %s", m.Tag)
	}
	if strings.TrimSpace(m.Upstream.Version) == "" || strings.TrimSpace(m.Upstream.Commit) == "" {
		return fmt.Errorf("批次清单缺少上游版本或提交，这批就说不清是搬自哪里: %s", m.Tag)
	}
	if len(m.Members) == 0 {
		return fmt.Errorf("批次清单没有任何成员: %s", m.Tag)
	}
	seen := make(map[string]bool, len(m.Members))
	for _, member := range m.Members {
		if err := member.Validate(); err != nil {
			return err
		}
		key := strings.TrimSpace(member.Kind) + "/" + strings.TrimSpace(member.ID)
		if seen[key] {
			return fmt.Errorf("批次清单里 %s 出现了两次", key)
		}
		seen[key] = true
	}
	return nil
}

// Validate 校验一个成员条目自洽。
func (m BatchMember) Validate() error {
	kind := strings.TrimSpace(m.Kind)
	if !KnownKind(kind) {
		return fmt.Errorf("批次成员类型不受支持: %q", m.Kind)
	}
	if err := ensureTagPart("id", m.ID); err != nil {
		return err
	}
	if err := ensureTagPart("version", m.Version); err != nil {
		return err
	}
	expected, err := ArtifactName(kind, m.ID, m.Version)
	if err != nil {
		return err
	}
	if strings.TrimSpace(m.Artifact.Name) != expected {
		return fmt.Errorf("%s/%s@%s 的制品名 %q 不是规范形式，应为 %q",
			kind, m.ID, m.Version, m.Artifact.Name, expected)
	}
	if m.Artifact.SizeBytes <= 0 {
		return fmt.Errorf("%s/%s@%s 的制品大小为 %d 字节", kind, m.ID, m.Version, m.Artifact.SizeBytes)
	}
	if err := ValidateSHA256("member artifact sha256", m.Artifact.SHA256); err != nil {
		return err
	}
	if strings.TrimSpace(m.Manifest) != ManifestName(m.ID, m.Version) {
		return fmt.Errorf("%s/%s@%s 的发布清单名 %q 不是规范形式，应为 %q",
			kind, m.ID, m.Version, m.Manifest, ManifestName(m.ID, m.Version))
	}
	return nil
}

// ManifestName 返回这条批次 Release 里总清单的文件名。
func (m BatchManifest) ManifestName() string {
	return BatchManifestName(m.Version)
}

// ReadBatchManifest 读取批次清单。
func ReadBatchManifest(path string) (BatchManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BatchManifest{}, err
	}
	var value BatchManifest
	if err := json.Unmarshal(data, &value); err != nil {
		return BatchManifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return value, nil
}

// WriteBatchManifest 写批次清单。同一份内容总是得到同样的字节，重复发布可以直接比对。
func WriteBatchManifest(path string, value BatchManifest) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
