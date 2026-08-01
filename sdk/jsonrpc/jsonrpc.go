package jsonrpc

import (
	"bufio"
	"encoding/json"
	"errors"
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
	Data    any    `json:"data,omitempty"`
}

type Handler func(Request) (any, *Error)

func NewRequest(id any, method string, params any) (Request, error) {
	idBytes, err := json.Marshal(id)
	if err != nil {
		return Request{}, err
	}
	paramBytes, err := json.Marshal(params)
	if err != nil {
		return Request{}, err
	}
	return Request{JSONRPC: "2.0", ID: idBytes, Method: method, Params: paramBytes}, nil
}

func Serve(r io.Reader, w io.Writer, handler Handler) error {
	if handler == nil {
		return errors.New("jsonrpc handler is required")
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	encoder := json.NewEncoder(w)
	for scanner.Scan() {
		var request Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
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
		response := Response{JSONRPC: "2.0", ID: request.ID, Result: result, Error: rpcError}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func DecodeParams(request Request, target any) *Error {
	if target == nil {
		return &Error{Code: -32602, Message: "params target is required"}
	}
	if len(request.Params) == 0 {
		return &Error{Code: -32602, Message: "params are required"}
	}
	if err := json.Unmarshal(request.Params, target); err != nil {
		return &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)}
	}
	return nil
}

func InvalidParams(message string) *Error {
	return &Error{Code: -32602, Message: message}
}

func InternalError(message string) *Error {
	return &Error{Code: -32000, Message: message}
}
