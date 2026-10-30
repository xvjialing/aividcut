package generationjobs

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"aividcut/internal/generation"
)

// EstimateInput 是人为核对的参考单价，不作为供应商账单或支出授权。
type EstimateInput struct {
	Currency  string `json:"currency"`
	Unit      string `json:"unit"`
	UnitPrice string `json:"unitPrice"`
}

type CostEstimate struct {
	Basis     string `json:"basis"`
	Currency  string `json:"currency"`
	Unit      string `json:"unit"`
	UnitPrice string `json:"unitPrice"`
	Quantity  int64  `json:"quantity"`
	Amount    string `json:"amount"`
}

var estimateDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})(\.[0-9]{1,6})?$`)

func estimateMoney(value int64) string {
	decimal := strings.TrimRight(fmt.Sprintf("%06d", value%1_000_000), "0")
	for len(decimal) < 2 {
		decimal += "0"
	}
	return fmt.Sprintf("%d.%s", value/1_000_000, decimal)
}

func calculateEstimate(request generation.Request, input *EstimateInput) (*CostEstimate, error) {
	if input == nil {
		return nil, nil
	}
	if input.Currency != "CNY" && input.Currency != "USD" {
		return nil, errors.New("费用参考仅支持 CNY 或 USD，不自动换算币种")
	}
	if len(input.UnitPrice) > 16 || !estimateDecimal.MatchString(input.UnitPrice) {
		return nil, errors.New("参考单价需要非负十进制数，最多九位整数和六位小数")
	}
	parts := strings.SplitN(input.UnitPrice, ".", 2)
	whole, _ := strconv.ParseInt(parts[0], 10, 64)
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	fraction += strings.Repeat("0", 6-len(fraction))
	micro, _ := strconv.ParseInt(fraction, 10, 64)
	micro += whole * 1_000_000
	count := int64(request.Count)
	if count == 0 {
		count = 1
	}
	if count < 1 || count > 1_000_000 {
		return nil, errors.New("费用参考的请求数量超出范围")
	}
	quantity := int64(1)
	switch input.Unit {
	case "request":
	case "image":
		if request.Mode != generation.ImageGenerate && request.Mode != generation.ImageEdit {
			return nil, errors.New("每张图片参考价仅用于图片生成或修改")
		}
		quantity = count
	case "video_second":
		if request.Mode != generation.VideoText && request.Mode != generation.VideoImage && request.Mode != generation.VideoReference {
			return nil, errors.New("每秒视频参考价仅用于视频生成")
		}
		if request.DurationSeconds < 1 || request.DurationSeconds > 1_000_000 || count > 1_000_000/int64(request.DurationSeconds) {
			return nil, errors.New("每秒视频参考价需要显式时长，且计价数量不超过一百万")
		}
		quantity = count * int64(request.DurationSeconds)
	default:
		return nil, errors.New("费用参考单位无效")
	}
	const maxInt64 = int64(1<<63 - 1)
	if micro > maxInt64/quantity {
		return nil, errors.New("费用参考金额超出当前计算范围")
	}
	return &CostEstimate{Basis: "manual-unit-v1", Currency: input.Currency, Unit: input.Unit, UnitPrice: estimateMoney(micro), Quantity: quantity, Amount: estimateMoney(micro * quantity)}, nil
}

func validEstimate(request generation.Request, value *CostEstimate) bool {
	if value == nil {
		return true
	}
	expected, err := calculateEstimate(request, &EstimateInput{Currency: value.Currency, Unit: value.Unit, UnitPrice: value.UnitPrice})
	return err == nil && reflect.DeepEqual(expected, value)
}
