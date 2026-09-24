package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	catalogtool "github.com/MrBaoquan/himind-extensions/tooling/catalog"
	"github.com/MrBaoquan/himind-extensions/tooling/distribution"
	"github.com/MrBaoquan/himind-extensions/tooling/skillproject"
	"github.com/MrBaoquan/himind-extensions/tooling/workflowproject"
	validator "github.com/MrBaoquan/himind-extensions/tools/cmd/himind-plugin-validate"
)

// catalogPath 是市场索引在仓库里的固定位置。
const catalogPath = ".himind/catalog.json"

type catalog struct {
	SchemaVersion              int                `json:"schema_version"`
	Repository                 string             `json:"repository"`
	DistributionID             string             `json:"distribution_id"`
	Channel                    string             `json:"channel"`
	CatalogID                  string             `json:"catalog_id"`
	DefaultBranch              string             `json:"default_branch"`
	DefaultDistributionTargets []string           `json:"default_distribution_targets"`
	Extensions                 []catalogExtension `json:"extensions"`
}

type catalogExtension struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

type manifestIdentity struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Author              string    `json:"author"`
	Categories          []string  `json:"categories"`
	Version             string    `json:"version"`
	ReleaseNotes        string    `json:"release_notes"`
	DistributionTargets *[]string `json:"distribution_targets"`
}

func main() {
	if err := run("."); err != nil {
		fmt.Fprintln(os.Stderr, "extension repository is invalid:", err)
		os.Exit(1)
	}
}

func run(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "extensions.json"))
	if err != nil {
		return err
	}
	var value catalog
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("extensions.json: %w", err)
	}
	if value.SchemaVersion != 1 || strings.TrimSpace(value.Repository) == "" || strings.TrimSpace(value.DefaultBranch) == "" {
		return errors.New("extensions.json must declare schema_version 1, repository and default_branch")
	}
	if !validDistributionID(value.DistributionID) || !validDistributionPart(value.Channel) || !validDistributionPart(value.CatalogID) {
		return errors.New("extensions.json must declare valid distribution_id, channel and catalog_id")
	}
	repoDefaults, err := distribution.Parse(value.DefaultDistributionTargets)
	if err != nil {
		return fmt.Errorf("extensions.json: %w", err)
	}
	if len(value.Extensions) == 0 {
		return errors.New("extensions.json contains no extensions")
	}
	seenIDs := map[string]string{}
	seenKinds := map[string]string{}
	seenPaths := map[string]string{}
	seenTargets := map[string][]distribution.Target{}
	for _, extension := range value.Extensions {
		if extension.Type != "plugin" && extension.Type != "skill" && extension.Type != "workflow" {
			return fmt.Errorf("unsupported extension type %q", extension.Type)
		}
		path, err := safePath(root, extension.Path)
		if err != nil {
			return err
		}
		identity, err := readManifest(path, extension.Type)
		if err != nil {
			return fmt.Errorf("%s: %w", extension.Path, err)
		}
		if identity.ID != extension.ID {
			return fmt.Errorf("%s declares id %q, catalog expects %q", extension.Path, identity.ID, extension.ID)
		}
		if previous, ok := seenIDs[identity.ID]; ok {
			return fmt.Errorf("duplicate extension id %q in %s and %s", identity.ID, previous, extension.Path)
		}
		if previous, ok := seenPaths[extension.Path]; ok {
			return fmt.Errorf("duplicate extension path %q for %s and %s", extension.Path, previous, identity.ID)
		}
		seenIDs[identity.ID] = extension.Path
		seenKinds[identity.ID] = extension.Type
		seenPaths[extension.Path] = identity.ID
		targets, err := declaredTargets(identity, repoDefaults)
		if err != nil {
			return fmt.Errorf("%s: %w", extension.Path, err)
		}
		seenTargets[identity.ID] = targets
		if strings.TrimSpace(identity.Name) == "" || strings.TrimSpace(identity.Version) == "" {
			return fmt.Errorf("%s must declare name, author, categories, version and release_notes", extension.Path)
		}
		if extension.Type == "plugin" {
			if strings.TrimSpace(identity.Author) == "" || len(identity.Categories) == 0 || strings.TrimSpace(identity.ReleaseNotes) == "" {
				return fmt.Errorf("%s must declare name, author, categories, version and release_notes", extension.Path)
			}
			if err := validator.ValidateDirectory(path); err != nil {
				return err
			}
		} else if extension.Type == "skill" {
			if strings.TrimSpace(identity.Author) == "" || len(identity.Categories) == 0 || strings.TrimSpace(identity.ReleaseNotes) == "" {
				return fmt.Errorf("%s must declare name, author, categories, version and release_notes", extension.Path)
			}
			if err := skillproject.Validate(path); err != nil {
				return err
			}
		} else if err := workflowproject.Validate(path); err != nil {
			return err
		}
	}
	if err := ensureCatalogComplete(root, "plugins", "plugin.json", seenPaths); err != nil {
		return err
	}
	if err := ensureCatalogComplete(root, "skills", "skill.json", seenPaths); err != nil {
		return err
	}
	if err := ensureCatalogComplete(root, "workflows", "workflow.json", seenPaths); err != nil {
		return err
	}
	if err := validateCatalog(root, seenKinds); err != nil {
		return err
	}
	ids := make([]string, 0, len(seenIDs))
	for id := range seenIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Printf("valid %s: %s [%s]\n", id, seenIDs[id], distribution.Describe(seenTargets[id]))
	}
	fmt.Printf("validated %d extensions\n", len(ids))
	return nil
}

// declaredTargets 读取清单里的分发落点。
//
// 清单必须显式声明，不从仓库默认值静默继承：落点决定制品发到哪里，作者
// 每次都要自己确认，避免新增扩展时「没写就等于默认」。仓库默认值只用于
// 脚手架和对照检查，未声明时在这里直接报错。
func declaredTargets(identity manifestIdentity, repoDefaults []distribution.Target) ([]distribution.Target, error) {
	if identity.DistributionTargets == nil {
		return nil, fmt.Errorf("must declare distribution_targets, repository default is [%s]", distribution.Describe(repoDefaults))
	}
	return distribution.Parse(*identity.DistributionTargets)
}

func validDistributionID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 160 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '.' && char != '_' && char != '-' && char != '/' {
			return false
		}
	}
	return true
}

func validDistributionPart(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func safePath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || strings.Contains(relative, "\\") {
		return "", fmt.Errorf("invalid catalog path %q", relative)
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("catalog path escapes repository: %q", relative)
	}
	return filepath.Join(root, clean), nil
}

func readManifest(root, kind string) (manifestIdentity, error) {
	name := "plugin.json"
	if kind == "skill" {
		name = "skill.json"
	} else if kind == "workflow" {
		name = "workflow.json"
	}
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return manifestIdentity{}, err
	}
	var value manifestIdentity
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	return value, nil
}

func ensureCatalogComplete(root, directory, manifest string, paths map[string]string) error {
	entries, err := os.ReadDir(filepath.Join(root, directory))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		relative := filepath.ToSlash(filepath.Join(directory, entry.Name()))
		if _, err := os.Stat(filepath.Join(root, relative, manifest)); err == nil {
			if _, ok := paths[relative]; !ok {
				return fmt.Errorf("%s is missing from extensions.json", relative)
			}
		}
	}
	return nil
}

// validateCatalog 校验市场索引确实是「规范命名」的派生物。
//
// 索引（`.himind/catalog.json`）不手写，只由发布脚本 upsert 或 himind-catalog-sync
// 全量重建。这里补两道廉价但有效的门禁：
//
//  1. 每条记录引用的 tag、制品名都符合 `distribution` 里的统一命名，并且带内嵌
//     签名——两套 tag 前缀或分离签名资产一旦混进来，安装器就会「发了但装不上」；
//  2. 索引里的记录必须指向 extensions.json 里真实存在的扩展，避免改名/删除后
//     留下指向空处的幽灵条目。
//
// 反向的「仓库里有、索引里没有」不算错误：新扩展可以先合并代码再发布，
// 与 himind-catalog-sync 的 -allow-missing 语义一致，这里只提示。
func validateCatalog(root string, kinds map[string]string) error {
	path := filepath.Join(root, filepath.FromSlash(catalogPath))
	index, err := catalogtool.Load(path)
	if err != nil {
		return fmt.Errorf("%s: %w", catalogPath, err)
	}
	published := map[string]bool{}
	for _, kind := range distribution.Kinds() {
		entries := index.Entries(kind)
		if entries == nil {
			return fmt.Errorf("%s: 缺少 %s 列表", catalogPath, kind)
		}
		for position, entry := range entries {
			where := fmt.Sprintf("%s: %s[%d]", catalogPath, kind, position)
			id := catalogtool.IDOf(kind, entry)
			if id == "" {
				return fmt.Errorf("%s 记录缺少 ID 字段", where)
			}
			where = where + " " + id
			version, err := catalogField(entry, "version")
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			where = fmt.Sprintf("%s@%s", where, version)
			declaredKind, ok := kinds[id]
			if !ok {
				return fmt.Errorf("%s 指向的扩展不在 extensions.json 里，索引存在幽灵条目", where)
			}
			if declaredKind != kind {
				return fmt.Errorf("%s 记录在 %s 列表，extensions.json 声明为 %s", where, kind, declaredKind)
			}
			tag, err := catalogField(entry, "release_tag")
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			tagKind, tagID, tagVersion, err := distribution.ParseReleaseTag(tag)
			if err != nil {
				return fmt.Errorf("%s: release_tag %q 不是规范形式: %w", where, tag, err)
			}
			if tagKind != kind || tagID != id || tagVersion != version {
				return fmt.Errorf("%s: release_tag %q 与记录自身不一致", where, tag)
			}
			wantArtifact, err := distribution.ArtifactName(kind, id, version)
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			fileName, err := catalogField(entry, "file_name")
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			if fileName == wantArtifact+".signature.json" || strings.HasSuffix(fileName, ".signature.json") {
				return fmt.Errorf("%s: 签名必须内嵌在发布清单，不能是独立资产 %q", where, fileName)
			}
			if fileName != wantArtifact {
				return fmt.Errorf("%s: file_name %q 不是规范制品名 %q", where, fileName, wantArtifact)
			}
			for _, field := range []string{"sha256", "artifact_sha256"} {
				value, err := catalogField(entry, field)
				if err != nil {
					return fmt.Errorf("%s: %w", where, err)
				}
				if len(value) != 64 {
					return fmt.Errorf("%s: %s 不是 64 位十六进制摘要: %q", where, field, value)
				}
			}
			if err := requireEmbeddedSignature(where, entry); err != nil {
				return err
			}
			downloadURL, err := catalogField(entry, "download_url")
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			if !strings.HasSuffix(downloadURL, "/releases/download/"+url.PathEscape(tag)+"/"+url.PathEscape(fileName)) {
				return fmt.Errorf("%s: download_url %q 未指向 %s 的规范资产", where, downloadURL, tag)
			}
			published[id] = true
		}
	}
	missing := make([]string, 0)
	for id := range kinds {
		if !published[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	for _, id := range missing {
		fmt.Printf("notice %s: 还没有规范发布，%s 里没有它的任何版本\n", id, catalogPath)
	}
	return nil
}

// requireEmbeddedSignature 确认条目带完整内嵌签名。
func requireEmbeddedSignature(where string, entry map[string]interface{}) error {
	signature, err := catalogField(entry, "signature")
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	keyID, err := catalogField(entry, "signature_key_id")
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	algorithm, err := catalogField(entry, "signature_algorithm")
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if algorithm != "rsa-pss-sha256" {
		return fmt.Errorf("%s: signature_algorithm 必须是 rsa-pss-sha256，当前为 %q", where, algorithm)
	}
	payload, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(payload) == 0 {
		return fmt.Errorf("%s: signature 不是有效的 base64 签名", where)
	}
	if strings.TrimSpace(keyID) == "" {
		return fmt.Errorf("%s: 缺少 signature_key_id", where)
	}
	return nil
}

// catalogField 读取索引记录里的必填字符串字段。
func catalogField(entry map[string]interface{}, name string) (string, error) {
	value, ok := entry[name]
	if !ok || value == nil {
		return "", fmt.Errorf("记录缺少 %s", name)
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return "", fmt.Errorf("记录的 %s 为空", name)
	}
	return text, nil
}
