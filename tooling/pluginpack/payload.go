package pluginpack

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PayloadFiles 返回插件目录里必须随包分发的文件，路径以 “/” 分隔且相对 root。
//
// 这是「什么进包」的唯一实现：本地开发包（extension.plugin.package）与发布制品
// （himind-plugin-stage → himind-plugin-package）都从这里取清单。曾经发布脚本用
// 一份硬编码白名单拼 staging，作者新加的运行期文件会在发布时被静默丢掉，出现
// 「本地能跑、发布装不上」；收敛成排除法之后，作者放进插件目录的文件默认都进包，
// 只有构建输入与生成物被排除。
func PayloadFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if shouldSkipPackageDirectory(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipPackageFile(info.Name()) {
			return nil
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// Generated dependency trees and local package-manager locks are never part of
// a portable HiMind plugin. Keeping them out makes packaging deterministic and
// prevents large UI plugins from timing out while being archived.
func shouldSkipPackageDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".github", "node_modules", "dist", "target", "test-output":
		return true
	default:
		return false
	}
}

// shouldSkipPackageFile 判断单个文件是不是构建输入或生成物。
//
// Go 源码与模块文件是构建输入：插件装到用户机器上只需要入口二进制和运行期资源，
// 源码进包只会泄露实现、放大制品。校验和文件与锁文件是生成物，每次打包都会重算。
// 发布制品（.hmpkg/.hmskill/.hmwf）与本地状态文件同理：插件目录里残留的旧包曾经
// 被整份塞进新包（software-distribution 因此从数 MB 涨到 43 MB），必须显式排除。
func shouldSkipPackageFile(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".go") {
		return true
	}
	for _, suffix := range []string{
		".hmpkg", ".hmskill", ".hmwf",
		".release-manifest.json",
		".pdb", ".log", ".tmp",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	switch lower {
	case "go.mod", "go.sum",
		"package-lock.json", "yarn.lock", "pnpm-lock.yaml",
		"checksums.sha256",
		"extension-lock.json",
		".gitignore", ".gitattributes", ".ds_store", "thumbs.db":
		return true
	default:
		return false
	}
}
