package mediadownload

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type douyinRoundTrip func(*http.Request) (*http.Response, error)

func (f douyinRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDouyinGatewayScopeAndIdentity(t *testing.T) {
	session := siteSession{Cookies: []siteCookie{
		{Name: "UIFID_TEMP", Value: "fixture-temporary", Domain: ".douyin.com"},
		{Name: "UIFID", Value: "fixture-primary", Domain: ".douyin.com"},
		{Name: "foreign", Value: "fixture-foreign", Domain: ".douyin.com.evil.test"},
		{Name: "expired", Value: "fixture-expired", Domain: ".douyin.com", Expires: time.Now().Unix() - 1},
		{Name: "bad", Value: "fixture\ninjection", Domain: ".douyin.com"},
	}}
	client := &http.Client{Transport: douyinRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://www.douyin.com/aweme/v1/web/aweme/detail/?aweme_id=123" || req.Header.Get("x-tt-argus") != "1" || req.Header.Get("uifid") != "fixture-primary" {
			t.Fatal("official endpoint or gateway headers incorrect")
		}
		if req.Header.Get("Referer") != "https://www.douyin.com/" || req.Header.Get("User-Agent") != douyinUserAgent {
			t.Fatal("desktop request headers missing")
		}
		cookies := req.Header.Get("Cookie")
		if !strings.Contains(cookies, "UIFID=fixture-primary") || strings.Contains(cookies, "foreign") || strings.Contains(cookies, "expired") || strings.Contains(cookies, "bad=") {
			t.Fatal("cookie scope violated")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"aweme_detail":{"aweme_id":"123","video":{"duration":1000}},"irrelevant":"discard"}`)), Request: req}, nil
	})}
	detail, err := fetchDouyinDetail(context.Background(), client, session, "123")
	if err != nil || !validDouyinDetail(detail, "123") || strings.Contains(string(detail), "irrelevant") || client.Jar != nil {
		t.Fatal("detail or client isolation failed", err)
	}
}

func TestDouyinGatewayRejectsRedirectMismatchAndRawErrors(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		status   int
		body     string
		redirect bool
	}{
		{"redirect", 302, "fixture-secret", true},
		{"rejection", 403, "fixture-secret", false},
		{"bad-json", 200, "fixture-secret", false},
		{"mismatch", 200, `{"aweme_detail":{"aweme_id":"456","video":{"duration":1}}}`, false},
		{"no-video", 200, `{"aweme_detail":{"aweme_id":"123","video":{}}}`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: douyinRoundTrip(func(req *http.Request) (*http.Response, error) {
				calls++
				h := make(http.Header)
				if fixture.redirect {
					h.Set("Location", "https://example.com/fixture-secret")
				}
				return &http.Response{StatusCode: fixture.status, Header: h, Body: io.NopCloser(strings.NewReader(fixture.body)), Request: req}, nil
			})}
			_, err := fetchDouyinDetail(context.Background(), client, siteSession{}, "123")
			if err == nil || calls != 1 || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("unsafe response accepted or exposed")
			}
		})
	}
}
