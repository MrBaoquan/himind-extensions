// Package packagedigest 计算扩展制品的载荷内容摘要。
//
// 依赖锁钉的是「依赖的哪一份内容」，而不是「哪一个制品文件」：同一个版本会随
// 安装来源落地成不同的本机文件集合——从本地扩展源安装会直接物化开发工作区
// （含源码、历史制品、安装期写入的 policy.json），从发布制品安装只落一份载荷。
// Agent 侧因此只在载荷范围内取摘要（himind-agent/src/workflow/store.rs::
// package_payload_digest）。这里复刻同一口径并且只认制品的实际字节，发布时写进
// 锁的摘要就会与任何一台机器从该制品安装后算出的值逐字节一致。
//
// 摘要形式：把载荷文件按路径排序，逐个累加「斜杠分隔的相对路径 \0 文件内容 \0」。
// 路径统一成斜杠分隔，避免同一份内容在不同平台算出两个值。
package packagedigest

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/pluginpack"
)

// 打包与签名过程生成的元数据文件。它们描述的是「制品怎么被打包的」，不是扩展
// 内容本身：同一个版本从本地目录安装和从归档安装，这些文件的字节并不相同。
const (
	checksumsFile = "checksums.sha256"
	signatureFile = "manifest.sig"
	// 本地安装期写入的来源、授权与治理状态，换台机器装一次就变一次。
	installMetadataFile = "policy.json"
)

// PayloadEntry 是一条进包内容：路径（斜杠分隔）与内容。
type PayloadEntry struct {
	Path    string
	Content []byte
}

// IsPackagingMetadata 判断路径是不是打包元数据（只在包根生效）。
func IsPackagingMetadata(path string) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	return normalized == checksumsFile || normalized == signatureFile
}

// IsInstallMetadata 判断路径是不是安装期写入的本机状态（只在包根生效）。
func IsInstallMetadata(path string) bool {
	return strings.ReplaceAll(path, "\\", "/") == installMetadataFile
}

// Digest 按载荷口径计算一组文件的摘要。入参顺序无关，路径会先归一化并排序。
func Digest(entries []PayloadEntry) string {
	digest := sha256.New()
	ordered := make([]PayloadEntry, len(entries))
	copy(ordered, entries)
	sort.Slice(ordered, func(first, second int) bool { return ordered[first].Path < ordered[second].Path })
	for _, entry := range ordered {
		digest.Write([]byte(entry.Path))
		digest.Write([]byte{0})
		digest.Write(entry.Content)
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// PayloadOfArchive 读出归档里属于可移植载荷的文件。归档目录条目与打包元数据、
// 安装期状态文件都不计入；不可移植路径（源码、依赖树、构建产物）与打包规则共用
// 同一份判断。
func PayloadOfArchive(path string) ([]PayloadEntry, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer reader.Close()
	entries := make([]PayloadEntry, 0, len(reader.File))
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		relative := strings.ReplaceAll(file.Name, "\\", "/")
		if IsPackagingMetadata(relative) || IsInstallMetadata(relative) || !pluginpack.PayloadPath(relative) {
			continue
		}
		content, err := readZipFile(file)
		if err != nil {
			return nil, fmt.Errorf("%s!%s: %w", path, relative, err)
		}
		entries = append(entries, PayloadEntry{Path: relative, Content: content})
	}
	return entries, nil
}

// PayloadDigestOfArchive 计算归档里可移植载荷的内容摘要。
func PayloadDigestOfArchive(path string) (string, error) {
	entries, err := PayloadOfArchive(path)
	if err != nil {
		return "", err
	}
	return Digest(entries), nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
