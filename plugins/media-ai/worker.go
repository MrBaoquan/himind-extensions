package main

import (
	"fmt"
	"os"
	"os/exec"

	"himind-plugin/media-ai/internal/media/job"
)

func startTranscriptionWorker(id string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "--transcription-worker", id)
	configureWorkerProcess(command)
	// Do not pass host credential brokers or media-stream signing secrets to a
	// background process. The worker only needs paths and its own data root.
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "PATH", "LOCALAPPDATA", "APPDATA", "USERPROFILE", "PROCESSOR_ARCHITECTURE", "HIMIND_PLUGIN_DATA_ROOT"} {
		if value, ok := os.LookupEnv(key); ok {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	if err = command.Start(); err != nil {
		return fmt.Errorf("无法启动本机识别工作进程: %w", err)
	}
	return command.Process.Release()
}

func runTranscriptionWorker(id string) error {
	store, err := job.NewStore()
	if err != nil {
		return err
	}
	var input transcribeInput
	if err = store.BackgroundInput(id, &input); err != nil {
		store.FailBackground(id, err)
		return err
	}
	service := &service{store: store, workerID: id}
	_, rpcError := service.transcribe(input)
	if rpcError != nil {
		err = fmt.Errorf("%s", rpcError.Message)
		store.FailBackground(id, err)
		return err
	}
	return nil
}
