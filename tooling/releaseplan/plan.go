// Package releaseplan 计算一次发布的命名与清单，是发布链路上唯一的「事实来源」。
//
// 仓库脚本（build / publish）不自己拼 tag 与制品名，只调用这里的 Plan；Publish
// 清单的依赖 pin 也从同一份 Plan 出来。这样「发布脚本算出的名字」与「安装器
// 解析的名字」不可能漂移。
package releaseplan

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/catalog"
	"github.com/MrBaoquan/himind-extensions/tooling/distribution"
)

// Plan 是一次发布的全部命名与内容事实。
type Plan struct {
	Kind            string                           `json:"kind"`
	ID              string                           `json:"id"`
	Name            string                           `json:"name"`
	Version         string                           `json:"version"`
	Channel         string                           `json:"channel"`
	Repository      string                           `json:"repository"`
	MinAgentVersion string                           `json:"min_agent_version"`
	Tag             string                           `json:"tag"`
	ArtifactName    string                           `json:"artifact_name"`
	Extension       string                           `json:"extension"`
	SignatureName   string                           `json:"signature_name"`
	ManifestName    string                           `json:"manifest_name"`
	LockName        string                           `json:"lock_name"`
	SourceCommit    string                           `json:"source_commit"`
	Dependencies    []distribution.ReleaseDependency `json:"dependencies"`
	DownloadURL     string                           `json:"download_url,omitempty"`
	ManifestURL     string                           `json:"manifest_url,omitempty"`
}

// Build 读取源码清单与市场索引，算出这次发布的命名与依赖 pin。
//
// catalogPath 允许不存在：此时索引视为空，依赖解析会按「索引里还没有发布」处理
// （必需依赖会因此阻断，而不是悄悄按最低版本发出去）。dry-run 场景直接把
// catalogPath 传空串即可。
func Build(kind, source, repository, channel, catalogPath string) (Plan, error) {
	if !distribution.KnownKind(kind) {
		return Plan{}, errors.New("kind 必须是 plugin、skill 或 workflow")
	}
	manifest, err := catalog.ReadManifest(source, kind)
	if err != nil {
		return Plan{}, err
	}
	if err := catalog.ValidateManifest(kind, manifest); err != nil {
		return Plan{}, err
	}
	tag, err := distribution.ReleaseTag(kind, manifest.ID, manifest.Version)
	if err != nil {
		return Plan{}, err
	}
	artifactName, err := distribution.ArtifactName(kind, manifest.ID, manifest.Version)
	if err != nil {
		return Plan{}, err
	}
	extension, err := distribution.ArtifactExtension(kind)
	if err != nil {
		return Plan{}, err
	}
	repository = strings.TrimSpace(repository)
	plan := Plan{
		Kind: kind, ID: manifest.ID, Name: manifest.Name, Version: manifest.Version,
		Channel: strings.TrimSpace(channel), Repository: repository,
		MinAgentVersion: manifest.MinAgentVersion,
		Tag:             tag, ArtifactName: artifactName, Extension: extension,
		SignatureName: distribution.SignatureName(artifactName),
		ManifestName:  distribution.ManifestName(manifest.ID, manifest.Version),
		LockName:      manifest.ID + "-" + manifest.Version + ".extension-lock.json",
	}
	if plan.Channel == "" {
		plan.Channel = "stable"
	}
	if repository != "" {
		plan.DownloadURL = distribution.DownloadURL(repository, tag, artifactName)
		plan.ManifestURL = distribution.DownloadURL(repository, tag, plan.ManifestName)
	}
	declared, err := catalog.DeclaredDependencies(kind, manifest)
	if err != nil {
		return Plan{}, err
	}
	if len(declared) == 0 {
		plan.Dependencies = []distribution.ReleaseDependency{}
		return plan, nil
	}
	var index *catalog.Catalog
	if strings.TrimSpace(catalogPath) == "" {
		index = catalog.New("", "", "")
	} else if _, err := os.Stat(catalogPath); err == nil {
		index, err = catalog.Load(catalogPath)
		if err != nil {
			return Plan{}, err
		}
	} else {
		index = catalog.New("", "", "")
	}
	pins, err := catalog.PinDependencies(declared, index, repository)
	if err != nil {
		return Plan{}, fmt.Errorf("%s@%s: %w", manifest.ID, manifest.Version, err)
	}
	plan.Dependencies = pins
	return plan, nil
}

// Manifest 用制品事实与签名补齐发布清单。
func (p Plan) Manifest(artifact distribution.ReleaseArtifact, signature *distribution.ReleaseSignature) distribution.ReleaseManifest {
	return distribution.ReleaseManifest{
		SchemaVersion:   distribution.ReleaseManifestSchema,
		Repository:      p.Repository,
		Tag:             p.Tag,
		Kind:            p.Kind,
		ID:              p.ID,
		Version:         p.Version,
		Channel:         p.Channel,
		SourceCommit:    p.SourceCommit,
		MinAgentVersion: p.MinAgentVersion,
		Artifact:        artifact,
		Dependencies:    p.Dependencies,
		Signature:       signature,
	}
}
