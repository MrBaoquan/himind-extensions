package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestServeDispatchesRequest(t *testing.T) {
	input := []byte(`{"jsonrpc":"2.0","id":1,"method":"echo","params":{"value":"ok"}}
`)
	var output bytes.Buffer
	err := Serve(bytes.NewReader(input), &output, func(request Request) (any, *Error) {
		var params struct {
			Value string `json:"value"`
		}
		if rpcError := DecodeParams(request, &params); rpcError != nil {
			return nil, rpcError
		}
		return map[string]string{"value": params.Value}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if string(response.ID) != "1" || response.Error != nil {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestServeReturnsProtocolErrors(t *testing.T) {
	input := []byte("not-json\n{\"jsonrpc\":\"1.0\",\"id\":2,\"method\":\"echo\"}\n")
	var output bytes.Buffer
	if err := Serve(bytes.NewReader(input), &output, func(Request) (any, *Error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected two protocol responses, got %d", len(lines))
	}
	for _, line := range lines {
		var response Response
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatal(err)
		}
		if response.Error == nil {
			t.Fatalf("expected protocol error: %s", line)
		}
	}
}

func TestNewRequestAndDecodeParams(t *testing.T) {
	request, err := NewRequest("req-1", "example.run", map[string]int{"count": 2})
	if err != nil {
		t.Fatal(err)
	}
	var params struct {
		Count int `json:"count"`
	}
	if rpcError := DecodeParams(request, &params); rpcError != nil {
		t.Fatal(rpcError)
	}
	if params.Count != 2 || request.Method != "example.run" {
		t.Fatalf("unexpected request: %+v", request)
	}
}

func TestServeAcceptsLargeImportAndContinuesDispatch(t *testing.T) {
	payload := strings.Repeat(`{"summary":"工作日志"}`, 80_000)
	request, err := NewRequest(7, "work-log.entry.import", map[string]string{"entries_json": payload})
	if err != nil {
		t.Fatal(err)
	}
	var input, output bytes.Buffer
	if err := json.NewEncoder(&input).Encode(request); err != nil {
		t.Fatal(err)
	}
	if input.Len() <= 1024*1024 {
		t.Fatal("regression input must exceed the old 1 MiB limit")
	}
	input.WriteString("{\"jsonrpc\":\"2.0\",\"id\":8,\"method\":\"echo\",\"params\":{}}\n")
	calls := 0
	err = Serve(&input, &output, func(request Request) (any, *Error) {
		calls++
		var params struct {
			EntriesJSON string `json:"entries_json"`
		}
		if rpcError := DecodeParams(request, &params); rpcError != nil {
			return nil, rpcError
		}
		if request.Method == "work-log.entry.import" && params.EntriesJSON != payload {
			t.Fatal("large imported JSON changed in transit")
		}
		return map[string]int{"bytes": len(params.EntriesJSON)}, nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("large import or following request failed: calls=%d err=%v", calls, err)
	}
	var first struct {
		Result struct {
			Bytes int `json:"bytes"`
		} `json:"result"`
	}
	if err := json.NewDecoder(&output).Decode(&first); err != nil || first.Result.Bytes != len(payload) {
		t.Fatalf("large import response mismatch: bytes=%d err=%v", first.Result.Bytes, err)
	}
}

func TestServeEnforcesRequestByteBoundaryBeforeDispatch(t *testing.T) {
	const prefix = `{"jsonrpc":"2.0","id":1,"method":"echo","params":{"payload":"`
	const suffix = `"}}`
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprintf("extra_bytes_%d", extra), func(t *testing.T) {
			input := prefix + strings.Repeat("x", MaxRequestBytes-len(prefix)-len(suffix)+extra) + suffix + "\n"
			called := false
			err := Serve(strings.NewReader(input), io.Discard, func(Request) (any, *Error) {
				called = true
				return nil, nil
			})
			if extra == 0 && (err != nil || !called) {
				t.Fatalf("exact-limit request failed: called=%v err=%v", called, err)
			}
			if extra > 0 && (!errors.Is(err, ErrRequestTooLarge) || called) {
				t.Fatalf("oversized request dispatched: called=%v err=%v", called, err)
			}
		})
	}
}
