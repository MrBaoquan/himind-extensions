package himindjsonrpc

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
}

type Handler func(Request) (any, *Error)

func Serve(r io.Reader, w io.Writer, handler Handler) error {
	if handler == nil { return errors.New("jsonrpc handler is required") }
	scanner := bufio.NewScanner(r)
	encoder := json.NewEncoder(w)
	for scanner.Scan() {
		var request Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			_ = encoder.Encode(Response{JSONRPC: "2.0", Error: &Error{Code: -32700, Message: "parse error"}})
			continue
		}
		result, rpcError := handler(request)
		if err := encoder.Encode(Response{JSONRPC: "2.0", ID: request.ID, Result: result, Error: rpcError}); err != nil { return err }
	}
	return scanner.Err()
}

func DecodeParams(request Request, target any) *Error {
	if target == nil { return &Error{Code: -32602, Message: "params target is required"} }
	if err := json.Unmarshal(request.Params, target); err != nil { return &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)} }
	return nil
}
