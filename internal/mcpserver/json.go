package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"
)

// 限制JSON深度/词元并拒绝重复键，避免SDK与业务解码器产生不同解释。
func checkedJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("JSON不是有效UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	count := 0
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		if depth > 64 || count > 200000 {
			return nil, errors.New("JSON结构超过限制")
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		count++
		delimiter, ok := token.(json.Delim)
		if !ok {
			return token, nil
		}
		switch delimiter {
		case '{':
			result := map[string]any{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				count++
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("JSON键无效")
				}
				if _, found := result[key]; found {
					return nil, errors.New("JSON键重复")
				}
				value, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				result[key] = value
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return result, nil
		case '[':
			result := []any{}
			for decoder.More() {
				value, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				result = append(result, value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return result, nil
		default:
			return nil, errors.New("JSON分隔符无效")
		}
	}
	value, err := read(0)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errors.New("JSON含多余数据")
	}
	return value, nil
}

type limitedResponse struct {
	header  http.Header
	code    int
	data    []byte
	maximum int64
	err     error
}

func newLimitedResponse(maximum int64) *limitedResponse {
	return &limitedResponse{header: make(http.Header), maximum: maximum}
}
func (response *limitedResponse) Header() http.Header { return response.header }
func (response *limitedResponse) WriteHeader(code int) {
	if response.code == 0 {
		response.code = code
	}
}
func (response *limitedResponse) Write(data []byte) (int, error) {
	if response.err != nil {
		return 0, response.err
	}
	if int64(len(data)) > response.maximum-int64(len(response.data)) {
		response.err = errors.New("响应超过限制")
		return 0, response.err
	}
	if response.code == 0 {
		response.code = http.StatusOK
	}
	response.data = append(response.data, data...)
	return len(data), nil
}

// JSON响应统一完成后发布；不把SDK刷新操作变成未核查的流式输出。
func (response *limitedResponse) Flush() {}
