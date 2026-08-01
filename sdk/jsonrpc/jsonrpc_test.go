package jsonrpc

import (
	"bytes"
	"encoding/json"
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
