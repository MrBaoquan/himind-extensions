// Package himindjsonrpc 是插件进程与 Agent 之间的 stdio JSON-RPC 编解码。
//
// 每个插件各自带一份：插件是独立 Go 模块，装到用户机器上时只需要入口二进制，
// 不能反向依赖本仓的 sdk 目录。
package himindjsonrpc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Handler func(Request) (any, *Error)

// Serve 逐行读取请求并逐行写回响应，直到 stdin 关闭。
func Serve(r io.Reader, w io.Writer, handler Handler) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	encoder := json.NewEncoder(w)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var request Request
		if err := json.Unmarshal(line, &request); err != nil {
			if err := encoder.Encode(Response{JSONRPC: "2.0", Error: &Error{Code: -32700, Message: "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if request.JSONRPC != "2.0" || request.Method == "" || len(request.ID) == 0 {
			if err := encoder.Encode(Response{JSONRPC: "2.0", ID: request.ID, Error: &Error{Code: -32600, Message: "invalid request"}}); err != nil {
				return err
			}
			continue
		}
		result, rpcError := handler(request)
		if err := encoder.Encode(Response{JSONRPC: "2.0", ID: request.ID, Result: result, Error: rpcError}); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// DecodeParams 把 params 解码到 target；空 params 视为空对象。
func DecodeParams(request Request, target any) *Error {
	if target == nil {
		return &Error{Code: -32602, Message: "params target is required"}
	}
	if len(request.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(request.Params, target); err != nil {
		return &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)}
	}
	return nil
}

func InvalidParams(message string) *Error {
	return &Error{Code: -32602, Message: message}
}

func Failed(message string) *Error {
	return &Error{Code: -32000, Message: message}
}
