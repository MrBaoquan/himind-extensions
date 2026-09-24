// Command himind-catalog-upsert 把一次发布写进市场索引。
//
// 输入是刚创建的 Release 清单：索引记录里的制品名、摘要、签名全部来自这份清单，
// 展示信息来自该版本的源码清单。脚本不重新推断任何字段，也就不存在
// 「索引说的」和「Release 上的」两份事实。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/tooling/catalog"
	"github.com/MrBaoquan/himind-extensions/tooling/distribution"
)

func main() {
	kind := flag.String("kind", "", "plugin, skill or workflow")
	source := flag.String("source", "", "扩展源码目录")
	releaseManifest := flag.String("release-manifest", "", "Release 上发布清单的本地副本")
	artifact := flag.String("artifact", "", "本地制品，提供时校验与清单一致")
	lock := flag.String("lock", "", "工作流扩展锁")
	catalogPath := flag.String("catalog", ".himind/catalog.json", "索引文件")
	publishedAt := flag.String("published-at", "", "RFC3339 发布时间，缺省取当前时间")
	sourceTree := flag.String("source-tree", "", "Git 源码树对象")
	repository := flag.String("repository", "", "GitHub owner/repo，覆盖发布清单里的写法")
	flag.Parse()
	if err := upsert(*kind, *source, *releaseManifest, *artifact, *lock, *catalogPath, *publishedAt, *sourceTree, *repository); err != nil {
		fmt.Fprintln(os.Stderr, "catalog update failed:", err)
		os.Exit(1)
	}
}

func upsert(kind, source, releaseManifestPath, artifactPath, lockPath, catalogPath, publishedAt, sourceTree, repository string) error {
	if !distribution.KnownKind(kind) {
		return fmt.Errorf("kind 必须是 plugin、skill 或 workflow")
	}
	for name, value := range map[string]string{"source": source, "release-manifest": releaseManifestPath} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("-%s 必填", name)
		}
	}
	if publishedAt == "" {
		publishedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if _, err := time.Parse(time.RFC3339, publishedAt); err != nil {
		return fmt.Errorf("-published-at: %w", err)
	}
	manifest, err := catalog.ReadManifest(source, kind)
	if err != nil {
		return err
	}
	if err := catalog.ValidateManifest(kind, manifest); err != nil {
		return err
	}
	release, err := distribution.ReadReleaseManifest(releaseManifestPath)
	if err != nil {
		return err
	}
	if err := release.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(artifactPath) != "" {
		if err := verifyArtifact(artifactPath, release); err != nil {
			return err
		}
	}
	var extensionLock json.RawMessage
	if kind == distribution.KindWorkflow {
		if strings.TrimSpace(lockPath) == "" {
			return fmt.Errorf("工作流发布必须提供 -lock")
		}
		data, err := os.ReadFile(lockPath)
		if err != nil {
			return err
		}
		if !json.Valid(data) {
			return fmt.Errorf("-lock 不是合法 JSON: %s", lockPath)
		}
		extensionLock = append(json.RawMessage(nil), data...)
	}
	target, err := catalog.Load(catalogPath)
	if err != nil {
		return err
	}
	entry, err := catalog.Entry(kind, manifest, catalog.ReleaseFacts{
		Release: release, PublishedAt: publishedAt, SourceTree: sourceTree,
		ExtensionLock: extensionLock, Repository: repository,
	})
	if err != nil {
		return err
	}
	if err := catalog.ValidateEntry(kind, entry, release); err != nil {
		return err
	}
	if err := target.Upsert(kind, entry); err != nil {
		return err
	}
	return target.Save(catalogPath)
}

// verifyArtifact 用清单里的摘要核对本地制品，避免把一个改过的包写进索引。
func verifyArtifact(artifactPath string, release distribution.ReleaseManifest) error {
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if release.Signature != nil && strings.TrimSpace(release.Signature.FileName) != "" {
		if release.Signature.FileName != filepath.Base(artifactPath) {
			return fmt.Errorf("本地制品名与发布清单签名不一致: %s", artifactPath)
		}
	}
	if !strings.EqualFold(hex.EncodeToString(digest[:]), release.Artifact.SHA256) {
		return fmt.Errorf("本地制品摘要与发布清单不一致: %s", artifactPath)
	}
	if release.Artifact.SizeBytes != int64(len(data)) {
		return fmt.Errorf("本地制品大小与发布清单不一致: %s", artifactPath)
	}
	return nil
}
