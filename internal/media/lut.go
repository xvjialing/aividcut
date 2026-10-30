package media

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxCubeBytes = 32 << 20

// ValidateCube 首轮仅接受 2–65 网格的 3D .cube，不隐式接受 1D/组合 LUT。
func ValidateCube(path string) (LUTInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return LUTInfo{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCubeBytes+1))
	if err != nil {
		return LUTInfo{}, err
	}
	if len(data) == 0 || len(data) > maxCubeBytes || !utf8.Valid(data) {
		return LUTInfo{}, errors.New("LUT 需要是非空 UTF-8 文件，且不超过 32 MiB")
	}
	return parseCube(data)
}

func parseCube(data []byte) (LUTInfo, error) {
	info := LUTInfo{DomainMax: [3]float64{1, 1, 1}}
	scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	seen := map[string]bool{}
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "TITLE ") || strings.HasPrefix(line, "TITLE\t") {
			if seen["TITLE"] || info.Entries > 0 {
				return info, fmt.Errorf("LUT 第 %d 行标题位置无效", lineNumber)
			}
			info.Title = strings.Trim(strings.TrimSpace(line[5:]), "\"")
			seen["TITLE"] = true
			continue
		}
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = strings.TrimSpace(line[:comment])
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case "LUT_1D_SIZE", "LUT_1D_INPUT_RANGE", "LUT_3D_INPUT_RANGE":
			return info, errors.New("首轮仅支持独立 3D .cube，不支持 1D、组合型或 INPUT_RANGE 变体")
		case "LUT_3D_SIZE":
			if len(parts) != 2 || seen[parts[0]] || info.Entries != 0 {
				return info, fmt.Errorf("LUT 第 %d 行网格声明无效", lineNumber)
			}
			var sizeErr error
			info.Size, sizeErr = strconv.Atoi(parts[1])
			if sizeErr != nil || info.Size < 2 || info.Size > 65 {
				return info, errors.New("3D LUT 网格必须在 2–65 之间")
			}
			seen[parts[0]] = true
		case "DOMAIN_MIN", "DOMAIN_MAX":
			if len(parts) != 4 || seen[parts[0]] || info.Entries != 0 {
				return info, fmt.Errorf("LUT 第 %d 行 DOMAIN 声明无效", lineNumber)
			}
			values, valueErr := triple(parts[1:])
			if valueErr != nil {
				return info, valueErr
			}
			if parts[0] == "DOMAIN_MIN" {
				info.DomainMin = values
			} else {
				info.DomainMax = values
			}
			seen[parts[0]] = true
		default:
			if info.Size == 0 || len(parts) != 3 {
				return info, fmt.Errorf("LUT 第 %d 行需要网格声明后的 RGB 三元数值", lineNumber)
			}
			if _, err := triple(parts); err != nil {
				return info, fmt.Errorf("LUT 第 %d 行: %w", lineNumber, err)
			}
			info.Entries++
			if info.Entries > info.Size*info.Size*info.Size {
				return info, errors.New("LUT 数据行数超过声明网格")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return info, fmt.Errorf("读取 LUT 失败: %w", err)
	}
	if info.Size == 0 || info.Entries != info.Size*info.Size*info.Size {
		return info, errors.New("LUT 数据行数与网格不一致")
	}
	for i := range info.DomainMin {
		if info.DomainMin[i] >= info.DomainMax[i] {
			return info, errors.New("LUT DOMAIN_MAX 必须大于 DOMAIN_MIN")
		}
	}
	return info, nil
}

func triple(parts []string) ([3]float64, error) {
	var result [3]float64
	for i, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return result, errors.New("LUT 数值必须为有限浮点数")
		}
		result[i] = value
	}
	return result, nil
}
