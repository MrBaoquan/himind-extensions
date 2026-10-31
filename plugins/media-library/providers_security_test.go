package main

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestProviderNetworkErrorsDoNotLeakRequestURL(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	request, err := http.NewRequest(http.MethodGet, "http://"+address+"/search?token=super-secret-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = getJSON(context.Background(), request, &struct{}{})
	if err == nil {
		t.Fatal("expected network error")
	}
	if strings.Contains(err.Error(), "super-secret-token") || strings.Contains(err.Error(), address) {
		t.Fatalf("network error leaked request URL: %v", err)
	}
}
