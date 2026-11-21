package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// utf8BOM 是 UTF-8 编码的字节序标记。
// Windows 下的编辑器保存 JSON 时可能带上该标记，encoding/json 会因此报语法错误，
// 所以在解析前统一剥离，避免用户遇到难以理解的报错。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Load 读取指定路径的 JSON 配置文件，完成解析、校验与默认值处理后返回运行配置。
//
// 返回的错误一律带有文件路径等上下文，便于 CLI 直接展示给用户。
// 任何错误都发生在探测开始之前，因此不需要也不允许进入重试流程。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %q 失败: %w", path, err)
	}

	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("配置文件 %q 无效: %w", path, err)
	}

	cfg.Source = path
	return cfg, nil
}

// Parse 从内存字节解析配置，供 Load 以及测试复用。
//
// 解析顺序为：剥离 BOM -> JSON 反序列化 -> 字段校验 -> 默认值填充 -> 转换为 model.Target。
// 校验会尽力收集所有目标的问题并一次性返回，避免用户反复修改、反复运行。
func Parse(data []byte) (*Config, error) {
	dto, err := decode(data)
	if err != nil {
		return nil, err
	}

	targets, err := validate(dto)
	if err != nil {
		return nil, err
	}

	return &Config{Targets: targets}, nil
}

// decode 把 JSON 字节反序列化为 fileConfig，并把底层错误翻译成可读信息。
func decode(data []byte) (*fileConfig, error) {
	data = bytes.TrimPrefix(data, utf8BOM)
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("JSON 解析失败：配置内容为空")
	}

	var dto fileConfig
	if err := json.Unmarshal(data, &dto); err != nil {
		return nil, decodeError(err)
	}
	return &dto, nil
}

// decodeError 把 encoding/json 的错误转换为面向用户的提示。
//
// 例如把 retry_count 写成字符串时，原始错误只提到内部结构体名称，这里改为
// 明确指出是哪个 JSON 字段类型不匹配。
func decodeError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "(根对象)"
		}
		return fmt.Errorf("JSON 字段 %s 类型不匹配：需要 %s，实际为 %s", field, typeErr.Type, typeErr.Value)
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("JSON 格式错误：%v（位置：第 %d 字节）", syntaxErr, syntaxErr.Offset)
	}

	return fmt.Errorf("JSON 解析失败：%w", err)
}
