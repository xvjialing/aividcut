package media

import "errors"

// EncodingResourceArgs是两种导出路径共用的白名单，不接受自由形式编码参数。
func EncodingResourceArgs(encoder, mode string) ([]string, error) {
	switch mode {
	case "", "standard":
		return nil, nil
	case "lower_memory":
		if encoder != "" && encoder != "libx264" {
			return nil, errors.New("较低内存档位仅支持H.264软件编码，请改用标准档位")
		}
		return []string{"-threads", "1", "-rc-lookahead", "4", "-x264-params", "sync-lookahead=0"}, nil
	default:
		return nil, errors.New("未知的导出编码资源档位")
	}
}
