package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "--worker" {
		if err := runWorker(os.Args[2]); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
	}
	if err := jsonrpc.Serve(os.Stdin, os.Stdout, handle); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "媒体下载工具已停止:", err)
	}
}

func handle(request jsonrpc.Request) (any, *jsonrpc.Error) {
	switch request.Method {
	case "media.downloader.runtime.status":
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return buildRuntimeStatus(ctx), nil
	case "media.downloader.runtime.install":
		var input struct {
			Force bool `json:"force"`
		}
		if rpcError := decodeOptionalParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		value, err := installRuntime(ctx, input.Force)
		if err != nil {
			return nil, rpcError(err)
		}
		return value, nil
	case "media.downloader.info.get":
		var input InfoInput
		if rpcError := jsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		location, err := locateYtDlp()
		if err != nil {
			return nil, rpcError(err)
		}
		value, err := probeMedia(ctx, location.Path, input)
		if err != nil {
			return nil, rpcError(err)
		}
		return value, nil
	case "media.downloader.job.start":
		var input JobStartInput
		if rpcError := jsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		value, err := startJob(ctx, input)
		if err != nil {
			return nil, rpcError(err)
		}
		return value, nil
	case "media.downloader.job.status":
		var input JobStatusInput
		if rpcError := jsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		value, err := getJobStatus(input.JobID)
		if err != nil {
			return nil, rpcError(err)
		}
		return value, nil
	case "media.downloader.job.cancel":
		var input JobStatusInput
		if rpcError := jsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		value, err := cancelJob(input.JobID)
		if err != nil {
			return nil, rpcError(err)
		}
		return value, nil
	case "media.downloader.job.retry":
		var input JobStatusInput
		if rpcError := jsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		value, err := retryJob(ctx, input.JobID)
		if err != nil {
			return nil, rpcError(err)
		}
		return value, nil
	case "media.downloader.job.remove":
		var input JobStatusInput
		if rpcError := jsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		value, err := removeJob(input.JobID)
		if err != nil {
			return nil, rpcError(err)
		}
		return value, nil
	case "media.downloader.job.cleanup":
		var input JobCleanupInput
		if rpcError := decodeOptionalParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		value, err := cleanupJobRecords(input)
		if err != nil {
			return nil, rpcError(err)
		}
		return value, nil
	case "media.downloader.job.log":
		var input JobLogInput
		if rpcError := jsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		value, err := getJobLog(input)
		if err != nil {
			return nil, rpcError(err)
		}
		return value, nil
	case "media.downloader.job.list":
		var input JobListInput
		if rpcError := decodeOptionalParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		value, err := listJobs(input)
		if err != nil {
			return nil, rpcError(err)
		}
		return value, nil
	default:
		return nil, &jsonrpc.Error{Code: -32602, Message: "不支持的媒体下载能力"}
	}
}

func decodeOptionalParams(request jsonrpc.Request, target any) *jsonrpc.Error {
	if len(request.Params) == 0 {
		request.Params = json.RawMessage(`{}`)
	}
	return jsonrpc.DecodeParams(request, target)
}

func rpcError(err error) *jsonrpc.Error {
	return &jsonrpc.Error{Code: -32000, Message: err.Error()}
}
