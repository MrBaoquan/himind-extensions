package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

func configureDownloadKernel() error {
	root, err := pluginDataRoot()
	if err != nil {
		return err
	}
	return mediadownload.Configure(mediadownload.Config{
		PluginID: dataNamespace, DataRoot: root, StrictDataRoot: true,
	})
}

func downloadCapability(request jsonrpc.Request) (any, error) {
	if err := configureDownloadKernel(); err != nil {
		return nil, err
	}
	switch request.Method {
	case "media-library.account.status":
		return mediadownload.ConnectionStatus()
	case "media-library.account.connect":
		return mediadownload.Connect()
	case "media-library.account.disconnect":
		return mediadownload.Disconnect()
	case "media-library.download.status":
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		status := mediadownload.Runtime(ctx)
		switch status.State {
		case "missing":
			status.Message = "下载组件未安装，请点击安装组件。"
		case "broken", "error":
			status.Message = "下载组件暂不可用，请重新安装或稍后重试。"
		}
		return status, nil
	case "media-library.download.install":
		var in struct {
			Force bool `json:"force"`
		}
		if err := json.Unmarshal(request.Params, &in); err != nil {
			return nil, fmt.Errorf("安装参数无效")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		return mediadownload.Install(ctx, in.Force)
	case "media-library.remote.resolve":
		var in mediadownload.InfoInput
		if err := json.Unmarshal(request.Params, &in); err != nil {
			return nil, fmt.Errorf("请输入媒体链接")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		info, err := mediadownload.Resolve(ctx, in)
		if err != nil {
			if strings.Contains(err.Error(), "site_connection_") {
				return nil, err
			}
			if code, message := mediadownload.DownloadFailure(err); code != "DOWNLOAD_FAILED" {
				return nil, fmt.Errorf("%s", message)
			}
			return nil, fmt.Errorf("链接解析失败，请检查链接、下载组件或平台连接后重试")
		}
		if err := rememberMusicUsage(remoteResource{SourceURL: info.WebpageURL, Title: info.Title, Author: info.Uploader}, info.Music); err != nil {
			return nil, err
		}
		return info, nil
	}
	return nil, fmt.Errorf("不支持的采集能力")
}
