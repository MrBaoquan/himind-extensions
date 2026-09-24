// Package catalog 维护 `.himind/catalog.json` 这一份市场索引。
//
// 索引是**派生数据**：真相在 GitHub Release（规范 tag + 制品 + 发布清单）。
// 因此本包只提供两种写入口——按一次发布增补（upsert），或按 Release 全量重建
// （sync）——不允许手改 JSON。两者的条目结构来自同一个 Entry 函数，
// 保证「发布的那个制品」与「索引里那条记录」永远是同一份事实。
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/tooling/distribution"
)

// SchemaVersion 是当前索引结构版本。
const SchemaVersion = 1

// Catalog 是市场索引文件。
type Catalog struct {
	SchemaVersion  int                      `json:"schema_version"`
	SourceID       string                   `json:"source_id"`
	DistributionID string                   `json:"distribution_id"`
	Channel        string                   `json:"channel"`
	CatalogID      string                   `json:"catalog_id"`
	Generation     string                   `json:"generation"`
	Plugins        []map[string]interface{} `json:"plugins"`
	Skills         []map[string]interface{} `json:"skills"`
	Workflows      []map[string]interface{} `json:"workflows"`
	FeaturePacks   []FeaturePack            `json:"feature_packs"`
}

// FeaturePack 是一组协同安装的扩展。
type FeaturePack struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	PluginIDs []string `json:"plugin_ids"`
	SkillIDs  []string `json:"skill_ids"`
}

// SignatureMetadata 与发布清单里的签名字段是同一份事实。
type SignatureMetadata = distribution.ReleaseSignature

// Manifest 是扩展源码清单里与索引相关的字段。
type Manifest struct {
	ID                 string                   `json:"id"`
	Name               string                   `json:"name"`
	Author             string                   `json:"author"`
	Categories         []string                 `json:"categories"`
	Description        string                   `json:"description"`
	Version            string                   `json:"version"`
	ReleaseNotes       string                   `json:"release_notes"`
	MinAgentVersion    string                   `json:"min_agent_version"`
	SupportedClients   []string                 `json:"supported_clients"`
	Capabilities       json.RawMessage          `json:"capabilities"`
	PluginDependencies []map[string]interface{} `json:"plugin_dependencies"`
	// Dependencies 是工作流源码清单里的依赖声明（skills / plugins / connectors / runtimes）。
	Dependencies map[string]interface{}   `json:"dependencies"`
	RiskSummary  string                   `json:"risk_summary"`
	Permissions  []string                 `json:"permissions"`
	Views        []map[string]interface{} `json:"views"`
}

// ReleaseFacts 是索引需要的、发布之外的补充事实：发布时间、源码树与工作流锁。
type ReleaseFacts struct {
	Release       distribution.ReleaseManifest
	PublishedAt   string
	SourceTree    string
	ExtensionLock json.RawMessage
	// Repository 覆盖发布清单里的仓库写法（清单可能写的是小写 owner）。
	Repository string
}

// ManifestFile 返回该类型的源码清单文件名。
func ManifestFile(kind string) (string, error) {
	switch kind {
	case distribution.KindPlugin:
		return "plugin.json", nil
	case distribution.KindSkill:
		return "skill.json", nil
	case distribution.KindWorkflow:
		return "workflow.json", nil
	default:
		return "", fmt.Errorf("不支持的扩展类型 %q", kind)
	}
}

// ReadManifest 读取扩展源码目录里的清单。
func ReadManifest(directory, kind string) (Manifest, error) {
	name, err := ManifestFile(kind)
	if err != nil {
		return Manifest{}, err
	}
	var value Manifest
	if err := ReadJSON(filepath.Join(directory, name), &value); err != nil {
		return Manifest{}, err
	}
	return value, nil
}

// ValidateManifest 校验索引所依赖的必需字段。
func ValidateManifest(kind string, value Manifest) error {
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.Name) == "" || strings.TrimSpace(value.Version) == "" {
		return errors.New("源码清单必须声明 id、name 与 version")
	}
	if kind != distribution.KindWorkflow && strings.TrimSpace(value.Author) == "" {
		return errors.New("源码清单必须声明 author")
	}
	return nil
}

// VerifyArtifact 校验签名元数据与本地制品逐字节一致。
func VerifyArtifact(artifactPath string, metadata SignatureMetadata) error {
	return distribution.VerifySignedArtifact(artifactPath, metadata)
}

// Load 读取索引文件。
func Load(path string) (*Catalog, error) {
	var value Catalog
	if err := ReadJSON(path, &value); err != nil {
		return nil, err
	}
	if value.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("catalog schema_version 必须是 %d", SchemaVersion)
	}
	return &value, nil
}

// New 返回一个空索引。
func New(distributionID, channel, catalogID string) *Catalog {
	if strings.TrimSpace(channel) == "" {
		channel = "stable"
	}
	if strings.TrimSpace(catalogID) == "" {
		catalogID = "public"
	}
	return &Catalog{
		SchemaVersion: SchemaVersion, DistributionID: strings.TrimSpace(distributionID),
		Channel: strings.TrimSpace(channel), CatalogID: strings.TrimSpace(catalogID),
		Plugins: []map[string]interface{}{}, Skills: []map[string]interface{}{},
		Workflows: []map[string]interface{}{}, FeaturePacks: DefaultFeaturePacks(),
	}
}

// Save 以固定格式写回索引文件，保证重复执行得到相同字节。
func (c *Catalog) Save(path string) error {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SchemaVersion
	}
	if c.Plugins == nil {
		c.Plugins = []map[string]interface{}{}
	}
	if c.Skills == nil {
		c.Skills = []map[string]interface{}{}
	}
	if c.Workflows == nil {
		c.Workflows = []map[string]interface{}{}
	}
	if len(c.FeaturePacks) == 0 {
		c.FeaturePacks = DefaultFeaturePacks()
	}
	c.RefreshGeneration()
	c.SortEntries()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// RefreshGeneration 由当前条目推出稳定的 generation，避免依赖执行时间。
func (c *Catalog) RefreshGeneration() {
	c.Generation = GenerationFor(c.AllEntries())
}

// SortEntries 让同一份输入总是产出同一份索引字节：先按 ID，再按版本倒序。
func (c *Catalog) SortEntries() {
	sortEntries(c.Plugins, "plugin_id")
	sortEntries(c.Skills, "skill_id")
	sortEntries(c.Workflows, "workflow_id")
}

// Entries 返回某类型的条目。
func (c *Catalog) Entries(kind string) []map[string]interface{} {
	switch kind {
	case distribution.KindPlugin:
		return c.Plugins
	case distribution.KindSkill:
		return c.Skills
	default:
		return c.Workflows
	}
}

// SetEntries 覆盖某类型的条目。
func (c *Catalog) SetEntries(kind string, entries []map[string]interface{}) {
	if entries == nil {
		entries = []map[string]interface{}{}
	}
	switch kind {
	case distribution.KindPlugin:
		c.Plugins = entries
	case distribution.KindSkill:
		c.Skills = entries
	default:
		c.Workflows = entries
	}
}

// Upsert 按 (kind, id, version) 覆盖一条记录。
func (c *Catalog) Upsert(kind string, entry map[string]interface{}) error {
	id, err := entryString(entry, idField(kind))
	if err != nil {
		return err
	}
	version, err := entryString(entry, "version")
	if err != nil {
		return err
	}
	c.SetEntries(kind, replaceVersion(c.Entries(kind), idField(kind), id, version, entry))
	return nil
}

// Entry 生成一条索引记录。
//
// 制品事实全部来自发布清单（Release 上的那份字节），展示信息全部来自该 tag 的
// 源码清单。两边的 id 与版本必须一致，否则这次发布不产生索引记录——
// 宁可让门禁报错，也不写入一条指向别处的事实。
func Entry(kind string, value Manifest, facts ReleaseFacts) (map[string]interface{}, error) {
	if !distribution.KnownKind(kind) {
		return nil, fmt.Errorf("不支持的扩展类型 %q", kind)
	}
	release := facts.Release
	if err := release.Validate(); err != nil {
		return nil, err
	}
	if release.Kind != kind {
		return nil, fmt.Errorf("发布清单类型 %q 与扩展类型 %q 不一致", release.Kind, kind)
	}
	if strings.TrimSpace(value.ID) != release.ID || strings.TrimSpace(value.Version) != release.Version {
		return nil, fmt.Errorf("源码清单 %s@%s 与发布清单 %s@%s 不是同一版本",
			value.ID, value.Version, release.ID, release.Version)
	}
	repository, err := NormalizeRepository(facts.Repository, release.Repository)
	if err != nil {
		return nil, err
	}
	channel := strings.TrimSpace(release.Channel)
	if channel == "" {
		channel = "stable"
	}
	if value.Categories == nil {
		value.Categories = []string{}
	}
	if value.PluginDependencies == nil {
		value.PluginDependencies = []map[string]interface{}{}
	}
	if value.Permissions == nil {
		value.Permissions = []string{}
	}
	if value.SupportedClients == nil {
		value.SupportedClients = []string{}
	}
	signed := SignatureMetadata{}
	if release.Signature != nil {
		signed = *release.Signature
	}
	entry := map[string]interface{}{
		"name": value.Name, "description": value.Description, "author_name": value.Author,
		"categories": value.Categories, "version": value.Version, "release_notes": value.ReleaseNotes,
		"published_at": NormalizePublishedAt(facts.PublishedAt), "min_agent_version": value.MinAgentVersion,
		"channel": channel, "artifact_id": "github:" + release.Tag + ":" + release.Artifact.Name,
		"file_name": release.Artifact.Name, "file_size": release.Artifact.SizeBytes,
		"sha256": release.Artifact.SHA256, "signature": signed.Signature,
		"signature_key_id": signed.SignatureKeyID, "signature_algorithm": signed.SignatureAlgorithm,
		"download_url": release.DownloadURLOf(repository),
		"source":       "github", "assignment": "optional", "management": "user_managed", "install_mode": "prompt",
		"organization_reason": "", "managed": false, "allow_disable": true, "allow_uninstall": true,
		"capability_ids": parseCapabilityIDs(value.Capabilities), "plugin_dependencies": value.PluginDependencies,
		"dependencies":  releaseDependencies(release),
		"source_commit": release.SourceCommit, "source_tree": facts.SourceTree, "release_tag": release.Tag,
		"artifact_sha256": release.Artifact.SHA256,
	}
	switch kind {
	case distribution.KindPlugin:
		entry["plugin_id"] = value.ID
		entry["review_status"] = "published"
		entry["governance"] = "optional"
		entry["permissions"] = value.Permissions
		entry["view_count"] = len(value.Views)
	case distribution.KindSkill:
		entry["skill_id"] = value.ID
		entry["supported_clients"] = value.SupportedClients
		entry["risk_summary"] = value.RiskSummary
	default:
		entry["workflow_id"] = value.ID
		entry["extension_lock"] = parseRawObject(facts.ExtensionLock)
	}
	return entry, nil
}

// ValidateEntry 校验一条索引记录与发布清单是同一次发布。
func ValidateEntry(kind string, entry map[string]interface{}, release distribution.ReleaseManifest) error {
	if err := release.Validate(); err != nil {
		return err
	}
	id, err := entryString(entry, idField(kind))
	if err != nil {
		return err
	}
	if id != release.ID {
		return fmt.Errorf("索引记录 %s 与发布清单 %s 不是同一扩展", id, release.ID)
	}
	version, err := entryString(entry, "version")
	if err != nil {
		return err
	}
	if version != release.Version {
		return fmt.Errorf("索引记录版本 %s 与发布清单版本 %s 不一致", version, release.Version)
	}
	if tag, err := entryString(entry, "release_tag"); err != nil || tag != release.Tag {
		return fmt.Errorf("索引记录 release_tag 与发布清单 tag 不一致: %s", release.Tag)
	}
	if name, err := entryString(entry, "file_name"); err != nil || name != release.Artifact.Name {
		return fmt.Errorf("索引记录 file_name 与发布清单制品名不一致: %s", release.Artifact.Name)
	}
	sha, err := entryString(entry, "sha256")
	if err != nil || !strings.EqualFold(sha, release.Artifact.SHA256) {
		return fmt.Errorf("索引记录 sha256 与发布清单摘要不一致: %s", release.Artifact.Name)
	}
	return nil
}

// NormalizeRepository 把仓库写法统一成 GitHub API 需要的 owner/repo。
func NormalizeRepository(override, fallback string) (string, error) {
	value := strings.TrimSpace(override)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	value = strings.TrimSuffix(value, ".git")
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "git@github.com:"} {
		value = strings.TrimPrefix(value, prefix)
	}
	value = strings.Trim(value, "/")
	if strings.Count(value, "/") != 1 {
		return "", fmt.Errorf("仓库不是 owner/repo 形式: %q", value)
	}
	return value, nil
}

// DefaultFeaturePacks 返回默认能力包。
func DefaultFeaturePacks() []FeaturePack {
	return []FeaturePack{{
		ID: "com.himind.feature.extension-authoring", Name: "扩展创作",
		PluginIDs: []string{"com.himind.extension-development-tools"},
		SkillIDs:  []string{"com.himind.skill.develop-himind-plugins", "com.himind.skill.develop-himind-skills"},
	}}
}

// ReadJSON 读取一个 JSON 文件。
func ReadJSON(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// ValidDistributionID 校验分发标识。
func ValidDistributionID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 160 {
		return false
	}
	for _, char := range value {
		if !isAlphaNumeric(char) && char != '.' && char != '_' && char != '-' && char != '/' {
			return false
		}
	}
	return true
}

// ValidDistributionPart 校验 channel / catalog 这类短标识。
func ValidDistributionPart(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !isAlphaNumeric(char) && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

// GenerationFor 由条目推出稳定的 generation：同一个索引内容总是得到同一个值。
// 取各条目的 (id, version, published_at, sha256) 排序后哈希，避免依赖执行时间。
func GenerationFor(entries []map[string]interface{}) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, strings.Join([]string{
			fmt.Sprint(entry["plugin_id"], entry["skill_id"], entry["workflow_id"]),
			fmt.Sprint(entry["version"]),
			fmt.Sprint(entry["published_at"]),
			fmt.Sprint(entry["sha256"]),
		}, "|"))
	}
	sort.Strings(lines)
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sync-" + hex.EncodeToString(digest[:])[:16]
}

// AllEntries 返回三类条目的合集，供 generation 计算与门禁遍历使用。
func (c *Catalog) AllEntries() []map[string]interface{} {
	entries := make([]map[string]interface{}, 0, len(c.Plugins)+len(c.Skills)+len(c.Workflows))
	entries = append(entries, c.Plugins...)
	entries = append(entries, c.Skills...)
	entries = append(entries, c.Workflows...)
	return entries
}

// IDOf 返回条目对应的扩展 ID；缺失时返回空串。
func IDOf(kind string, entry map[string]interface{}) string {
	value, _ := entryString(entry, idField(kind))
	return value
}

// KindOf 按条目里出现的 ID 字段判断类型。
func KindOf(entry map[string]interface{}) string {
	switch {
	case entry["plugin_id"] != nil:
		return distribution.KindPlugin
	case entry["skill_id"] != nil:
		return distribution.KindSkill
	case entry["workflow_id"] != nil:
		return distribution.KindWorkflow
	default:
		return ""
	}
}

// NormalizePublishedAt 把发布时间统一成 UTC RFC3339，保证索引字节稳定。
func NormalizePublishedAt(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(time.RFC3339)
}

func idField(kind string) string {
	switch kind {
	case distribution.KindPlugin:
		return "plugin_id"
	case distribution.KindSkill:
		return "skill_id"
	default:
		return "workflow_id"
	}
}

func entryString(entry map[string]interface{}, field string) (string, error) {
	value, ok := entry[field]
	if !ok {
		return "", fmt.Errorf("索引记录缺少 %s", field)
	}
	text := fmt.Sprint(value)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("索引记录的 %s 为空", field)
	}
	return text, nil
}

func replaceVersion(items []map[string]interface{}, idField, id, version string, entry map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(items)+1)
	for _, item := range items {
		if fmt.Sprint(item[idField]) == id && fmt.Sprint(item["version"]) == version {
			continue
		}
		result = append(result, item)
	}
	return append(result, entry)
}

func sortEntries(items []map[string]interface{}, idField string) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := fmt.Sprint(items[i][idField]), fmt.Sprint(items[j][idField])
		if left != right {
			return left < right
		}
		return CompareVersions(fmt.Sprint(items[i]["version"]), fmt.Sprint(items[j]["version"])) > 0
	})
}

// CompareVersions 按数字段比较版本，保证 1.10.0 排在 1.9.0 之前。
func CompareVersions(left, right string) int {
	parse := func(value string) []int {
		value = strings.SplitN(value, "-", 2)[0]
		value = strings.SplitN(value, "+", 2)[0]
		parts := strings.Split(value, ".")
		numbers := make([]int, 0, len(parts))
		for _, part := range parts {
			number := 0
			valid := part != ""
			for _, char := range part {
				if char < '0' || char > '9' {
					valid = false
					break
				}
				number = number*10 + int(char-'0')
			}
			if !valid {
				number = 0
			}
			numbers = append(numbers, number)
		}
		return numbers
	}
	leftParts, rightParts := parse(left), parse(right)
	for index := 0; index < len(leftParts) || index < len(rightParts); index++ {
		var a, b int
		if index < len(leftParts) {
			a = leftParts[index]
		}
		if index < len(rightParts) {
			b = rightParts[index]
		}
		if a != b {
			if a > b {
				return 1
			}
			return -1
		}
	}
	return 0
}

func parseCapabilityIDs(raw json.RawMessage) []string {
	var objects []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &objects); err == nil {
		result := make([]string, 0, len(objects))
		for _, item := range objects {
			if item.ID != "" {
				result = append(result, item.ID)
			}
		}
		return result
	}
	var stringsOnly []string
	if err := json.Unmarshal(raw, &stringsOnly); err == nil {
		result := make([]string, 0, len(stringsOnly))
		for _, item := range stringsOnly {
			if strings.TrimSpace(item) != "" {
				result = append(result, item)
			}
		}
		return result
	}
	return []string{}
}

func parseRawArray(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return []interface{}{}
	}
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return []interface{}{}
	}
	if value == nil {
		return []interface{}{}
	}
	return value
}

// releaseDependencies 把清单里的依赖 pin 转成索引里的数组，缺省是空数组。
func releaseDependencies(release distribution.ReleaseManifest) interface{} {
	items := release.DependencyList()
	if items == nil {
		return []interface{}{}
	}
	return items
}

func parseRawObject(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

func isAlphaNumeric(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
}
