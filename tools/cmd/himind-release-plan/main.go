// Command himind-release-plan 打印一次发布的命名事实，并可按同一份事实写发布清单。
//
// 仓库脚本按它决定 tag、制品名、清单名、锁名与依赖 pin，命名规则只有
// tooling/distribution 一份实现，脚本不再自己拼字符串。带上 -artifact 与
// -manifest-out 时，它同时产出 Release 上要上传的 `<id>@<version>.json`：
// 制品摘要取本地制品，签名取本地签名元数据，依赖 pin 取市场索引。
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

	"github.com/MrBaoquan/himind-extensions/tooling/distribution"
	"github.com/MrBaoquan/himind-extensions/tooling/releaseplan"
)

func main() {
	kind := flag.String("kind", "", "plugin, skill or workflow")
	source := flag.String("path", "", "扩展源码目录")
	repository := flag.String("repository", "", "GitHub owner/repo，用于拼下载地址")
	channel := flag.String("channel", "stable", "发布通道")
	catalogPath := flag.String("catalog", ".himind/catalog.json", "市场索引，用于解析依赖 pin；文件不存在时视为空索引")
	artifactPath := flag.String("artifact", "", "本地制品，写发布清单时必填")
	signaturePath := flag.String("signature", "", "本地签名元数据，提供时内嵌进发布清单")
	manifestOut := flag.String("manifest-out", "", "发布清单输出路径；缺省只打印命名事实")
	sourceCommit := flag.String("source-commit", "", "源码提交，写进发布清单")
	flag.Parse()

	if err := run(*kind, *source, *repository, *channel, *catalogPath,
		*artifactPath, *signaturePath, *manifestOut, *sourceCommit); err != nil {
		fmt.Fprintln(os.Stderr, "release plan failed:", err)
		os.Exit(1)
	}
}

func run(kind, source, repository, channel, catalogPath, artifactPath, signaturePath, manifestOut, sourceCommit string) error {
	plan, err := releaseplan.Build(kind, source, repository, channel, catalogPath)
	if err != nil {
		return err
	}
	plan.SourceCommit = strings.TrimSpace(sourceCommit)
	if strings.TrimSpace(manifestOut) == "" {
		return printJSON(plan)
	}
	if strings.TrimSpace(artifactPath) == "" {
		return fmt.Errorf("写发布清单需要 -artifact")
	}
	artifact, err := describeArtifact(artifactPath)
	if err != nil {
		return err
	}
	// 签名元数据必须与本地制品逐字节一致，否则这次发布会在安装侧被拒绝。
	var signature *distribution.ReleaseSignature
	if strings.TrimSpace(signaturePath) != "" {
		value, err := distribution.SignatureFromArtifact(artifactPath, signaturePath)
		if err != nil {
			return err
		}
		signature = &value
	}
	manifest := plan.Manifest(artifact, signature)
	if err := manifest.Validate(); err != nil {
		return err
	}
	if err := distribution.WriteReleaseManifest(manifestOut, manifest); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "release manifest %s: %s (%d bytes, signed=%t)\n",
		manifestOut, manifest.Tag, artifact.SizeBytes, signature != nil)
	output := struct {
		releaseplan.Plan
		ManifestPath string `json:"manifest_path"`
	}{Plan: plan, ManifestPath: manifestOut}
	return printJSON(output)
}

// describeArtifact 用本地制品填清单里的制品事实。
func describeArtifact(path string) (distribution.ReleaseArtifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return distribution.ReleaseArtifact{}, err
	}
	digest := sha256.Sum256(data)
	return distribution.ReleaseArtifact{
		Name:      filepath.Base(path),
		SizeBytes: int64(len(data)),
		SHA256:    hex.EncodeToString(digest[:]),
	}, nil
}

func printJSON(value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
