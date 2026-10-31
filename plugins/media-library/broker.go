package main

// Agent 凭据 Broker 客户端。插件只在单次能力调用内取得所需来源凭据，
// 不把长期密钥写入插件数据目录。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	brokerURLEnd   = "HIMIND_CREDENTIAL_BROKER_URL"
	brokerTokenEnv = "HIMIND_CREDENTIAL_BROKER_TOKEN"
)

type credentialBrokerError struct {
	Code    string
	Message string
	Status  int
}

func (e *credentialBrokerError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return "凭据不可用"
}

var brokerHTTPClient = &http.Client{Timeout: 12 * time.Second}

func acquireCredential(credentialID string, purpose string) (map[string]string, error) {
	endpoint := strings.TrimSpace(os.Getenv(brokerURLEnd))
	token := strings.TrimSpace(os.Getenv(brokerTokenEnv))
	if endpoint == "" || token == "" {
		return nil, &credentialBrokerError{
			Code:    "broker_unavailable",
			Message: "请先在 HiMind Agent 的账号与凭据中配置该来源",
		}
	}
	credentialID = strings.TrimSpace(credentialID)
	purpose = strings.TrimSpace(purpose)
	if credentialID == "" || purpose == "" {
		return nil, &credentialBrokerError{Code: "invalid_request", Message: "凭据标识或用途为空"}
	}
	body, err := json.Marshal(map[string]string{
		"credential_id": credentialID,
		"purpose":       purpose,
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := brokerHTTPClient.Do(request)
	if err != nil {
		return nil, &credentialBrokerError{Code: "broker_unreachable", Message: "无法连接 Agent 凭据服务"}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(payload, &failure)
		message := strings.TrimSpace(failure.Message)
		if message == "" {
			switch response.StatusCode {
			case http.StatusUnauthorized:
				message = "凭据 Broker 会话已失效"
			case http.StatusForbidden:
				message = "插件未获准访问该凭据"
			case http.StatusConflict:
				message = "凭据未配置或声明已变更，请在 HiMind Agent 的账号与凭据中重新确认"
			default:
				message = fmt.Sprintf("凭据服务返回 HTTP %d", response.StatusCode)
			}
		}
		return nil, &credentialBrokerError{Code: failure.Error, Message: message, Status: response.StatusCode}
	}
	var result struct {
		Fields map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, &credentialBrokerError{Code: "invalid_broker_response", Message: "凭据服务返回了无法解析的响应"}
	}
	return result.Fields, nil
}

func credentialState(err error) (string, string) {
	if err == nil {
		return "configured", ""
	}
	var brokerErr *credentialBrokerError
	if ok := asCredentialBrokerError(err, &brokerErr); ok {
		switch brokerErr.Status {
		case http.StatusConflict:
			return "unconfigured", brokerErr.Message
		case http.StatusForbidden:
			return "permission_denied", brokerErr.Message
		case http.StatusUnauthorized:
			return "session_expired", brokerErr.Message
		}
		if brokerErr.Code == "broker_unavailable" || brokerErr.Code == "broker_unreachable" {
			return "unconfigured", brokerErr.Message
		}
	}
	return "error", err.Error()
}

func asCredentialBrokerError(err error, target **credentialBrokerError) bool {
	for err != nil {
		if value, ok := err.(*credentialBrokerError); ok {
			*target = value
			return true
		}
		type unwrapper interface{ Unwrap() error }
		wrapped, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = wrapped.Unwrap()
	}
	return false
}
