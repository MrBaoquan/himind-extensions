//go:build !windows

package filepicker

import (
	"context"
	"errors"
)

func pickPaths(context.Context, dialogRequest) ([]string, error) {
	return nil, errors.New("当前平台不支持原生文件选择器")
}
