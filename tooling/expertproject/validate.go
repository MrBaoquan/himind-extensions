// Package expertproject 校验专家目录是否符合 expert.v1 约定。
//
// 判据与 Agent 侧 src/expert.rs 的 validate() 对齐：专家包在运行期会被逐字段
// 校验，仓库门禁必须用同一套规则，否则会出现「仓里能过、装的时候才报」。
package expertproject

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SchemaVersion 是当前唯一受支持的专家定义版本。
const SchemaVersion = "expert.v1"

// MaxInstructionsBytes 与 Agent 侧 128 KiB 上限保持一致。
const MaxInstructionsBytes = 131072

// Manifest 是仓库里 expert.json 的最小校验视图。
type Manifest struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Author        string   `json:"author"`
	Categories    []string `json:"categories"`
	Version       string   `json:"version"`
	ReleaseNotes  string   `json:"release_notes"`
	Description   string   `json:"description"`
	Instructions  string   `json:"instructions"`
	Contents      []string `json:"contents"`
}

// Validate 校验专家目录：专家定义与它声明的正文文件都在位。
func Validate(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "expert.json"))
	if err != nil {
		return err
	}
	var value Manifest
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("expert.json: %w", err)
	}
	return ValidateManifest(root, value)
}

// ValidateManifest 在已解析出清单的前提下做字段与内容检查。
func ValidateManifest(root string, value Manifest) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported expert schema_version %q", value.SchemaVersion)
	}
	if err := validateID(value.ID); err != nil {
		return err
	}
	if err := validateVersion(value.Version); err != nil {
		return err
	}
	if strings.TrimSpace(value.Instructions) == "" {
		return errors.New("expert instructions is required")
	}
	if len(value.Instructions) > MaxInstructionsBytes {
		return errors.New("expert instructions exceeds 128 KiB")
	}
	if len(value.Contents) != 1 || value.Contents[0] != "EXPERT.md" {
		return errors.New("expert contents must declare exactly EXPERT.md")
	}
	// 声明的正文必须真的在目录里：Agent 按 contents 取文件，缺文件留到运行期
	// 才报就太晚了。
	info, err := os.Stat(filepath.Join(root, "EXPERT.md"))
	if err != nil || info.IsDir() {
		return errors.New("expert content is missing: EXPERT.md")
	}
	return nil
}

// validateID 与 Agent 侧专家 ID 规则一致：长度上限 160，只允许 ASCII
// 字母数字与 . - _，且不能是 . 或 ..。
func validateID(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "." || trimmed == ".." || len(trimmed) > 160 {
		return fmt.Errorf("invalid expert id: %s", value)
	}
	for _, r := range trimmed {
		if !isASCIIAlnum(r) && r != '.' && r != '-' && r != '_' {
			return fmt.Errorf("invalid expert id: %s", value)
		}
	}
	return nil
}

// validateVersion 与 Agent 侧一致：只允许 ASCII 字母数字与 . - +。
func validateVersion(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return fmt.Errorf("invalid expert version: %s", value)
	}
	for _, r := range trimmed {
		if !isASCIIAlnum(r) && r != '.' && r != '-' && r != '+' {
			return fmt.Errorf("invalid expert version: %s", value)
		}
	}
	return nil
}

func isASCIIAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
