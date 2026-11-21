package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"service-health-checker/internal/model"
)

// validate 校验全部目标，并把通过校验的 DTO 转换为 model.Target。
//
// 校验会收集所有目标的问题后一次性返回（而不是遇到第一个错误就退出），
// 每条问题都带上 targets 索引、目标名称与字段名，便于用户定位。
func validate(dto *fileConfig) ([]model.Target, error) {
	if dto == nil {
		return nil, errors.New("配置校验失败：配置内容为空")
	}
	if len(dto.Targets) == 0 {
		return nil, errors.New("配置校验失败：targets 不能为空，至少需要配置一个探测目标")
	}

	targets := make([]model.Target, 0, len(dto.Targets))
	var problems []string

	for i := range dto.Targets {
		target, errs := normalizeTarget(i, dto.Targets[i])
		problems = append(problems, errs...)
		targets = append(targets, target)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("配置校验失败，共 %d 处问题：\n  - %s", len(problems), strings.Join(problems, "\n  - "))
	}
	return targets, nil
}

// normalizeTarget 校验单个目标并转换为 model.Target，同时返回该目标的全部问题。
//
// 返回值中的 target 即使存在校验问题也会尽量填充已解析的字段，便于调用方
// 打印上下文；但存在问题时 validate 会整体返回错误，不会把它交给调度层。
func normalizeTarget(index int, tc targetConfig) (model.Target, []string) {
	name := strings.TrimSpace(tc.Name)
	address := strings.TrimSpace(tc.Address)

	label := fmt.Sprintf("targets[%d]", index)
	if name != "" {
		label = fmt.Sprintf("targets[%d]（%s）", index, name)
	}

	var problems []string
	report := func(field, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("%s 字段 %s：%s", label, field, fmt.Sprintf(format, args...)))
	}

	if name == "" {
		report("name", "不能为空")
	}
	if address == "" {
		report("address", "不能为空")
	}

	// retry_count 缺省等价于 0（不重试）；显式填写时必须在 0～3 之间。
	retryCount := 0
	if tc.RetryCount != nil {
		retryCount = *tc.RetryCount
		if retryCount < minRetryCount || retryCount > maxRetryCount {
			report("retry_count", "必须在 %d～%d 之间，当前为 %d", minRetryCount, maxRetryCount, retryCount)
		}
	}

	protocol := strings.ToLower(strings.TrimSpace(tc.Type))
	target := model.Target{
		Name:       name,
		Type:       protocol,
		Address:    address,
		RetryCount: retryCount,
	}

	switch protocol {
	case "":
		report("type", "不能为空，只支持 %q 或 %q", model.TypeHTTP, model.TypeTCP)
	case model.TypeHTTP:
		target.ExpectedStatus = normalizeExpectedStatus(tc.ExpectedStatus, report)
		if tc.Contains != nil {
			target.Contains = *tc.Contains
		}
		if address != "" {
			if err := validateHTTPAddress(address); err != nil {
				report("address", "%s", err)
			}
		}
	case model.TypeTCP:
		// TCP 不使用 HTTP 专属字段：显式出现即视为配置含义不清，直接拒绝。
		if tc.ExpectedStatus != nil {
			report("expected_status", "不适用于 TCP 目标，请删除该字段")
		}
		if tc.Contains != nil {
			report("contains", "不适用于 TCP 目标，请删除该字段")
		}
		if address != "" {
			if err := validateTCPAddress(address); err != nil {
				report("address", "%s", err)
			}
		}
	default:
		// 未知协议不得静默映射成有效类型，必须报错。
		report("type", "不支持协议 %q，只支持 %q 或 %q", tc.Type, model.TypeHTTP, model.TypeTCP)
	}

	return target, problems
}

// normalizeExpectedStatus 处理 HTTP 期望状态码的默认值。
//
// 字段缺省或显式填写 0 都视为"未指定"，统一取默认值 200；其余取值必须在
// 100～599 之间。doc 6.1 要求"expected_status 为 0 时设置为 200"，
// 而 doc 7.2 要求合法区间为 100～599，把 0 解释为缺省可同时满足两处约定。
func normalizeExpectedStatus(raw *int, report func(field, format string, args ...any)) int {
	if raw == nil || *raw == 0 {
		return defaultExpectedStatus
	}

	status := *raw
	if status < minHTTPStatus || status > maxHTTPStatus {
		report("expected_status", "必须在 %d～%d 之间，当前为 %d", minHTTPStatus, maxHTTPStatus, status)
	}
	return status
}

// validateHTTPAddress 校验 HTTP 目标地址：必须是 http/https 的完整 URL 且带主机名。
func validateHTTPAddress(address string) error {
	u, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("不是合法的 URL：%v", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("URL 的 scheme 只支持 http 或 https，当前为 %q", u.Scheme)
	}

	if u.Hostname() == "" {
		return errors.New("URL 缺少主机名（host）")
	}
	return nil
}

// validateTCPAddress 校验 TCP 目标地址：必须能解析出非空 host 和 1～65535 的端口。
//
// IPv6 地址需使用标准方括号格式，例如 [::1]:3306。
func validateTCPAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("必须为 host:port 形式（IPv6 请使用 [::1]:3306）：%v", err)
	}

	if strings.TrimSpace(host) == "" {
		return errors.New("缺少主机地址（host）")
	}

	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("端口 %q 不是合法的整数", portText)
	}
	if port < minPort || port > maxPort {
		return fmt.Errorf("端口必须在 %d～%d 之间，当前为 %d", minPort, maxPort, port)
	}
	return nil
}
