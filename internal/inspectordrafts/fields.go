// Package inspectordrafts 只处理明确登记的属性输入，不接受任意字段路径或工程写入。
package inspectordrafts

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"aividcut/internal/editor"
)

type Field struct {
	Key  string          `json:"key"`
	Raw  string          `json:"raw"`
	Base json.RawMessage `json:"base"`
}
type Difference struct {
	Key      string          `json:"key"`
	Label    string          `json:"label"`
	Raw      string          `json:"raw"`
	Base     json.RawMessage `json:"base"`
	Current  json.RawMessage `json:"current"`
	Conflict bool            `json:"conflict"`
	Error    string          `json:"error,omitempty"`
}

var labels = map[string]string{"projectName": "工程名称", "name": "片段名称", "text": "显示内容", "startFrame": "起点 · 帧", "durationFrames": "长度 · 帧", "sourceInUs": "素材入点 · 秒", "fontSize": "字号 · px", "x": "水平位移 · px", "y": "垂直位移 · px", "scale": "缩放", "rotation": "旋转 · °", "volume": "音量 · %", "fadeInUs": "淡入 · 秒", "fadeOutUs": "淡出 · 秒", "lutIntensity": "LUT 强度 · %"}

func ValidateFields(fields []Field) error {
	if len(fields) > 14 {
		return errors.New("属性草稿字段过多")
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if labels[f.Key] == "" || seen[f.Key] || len(f.Raw) > 64<<10 || len(f.Base) > 64<<10 {
			return errors.New("属性草稿字段、大小或基值无效")
		}
		seen[f.Key] = true
		var value any
		if json.Unmarshal(f.Base, &value) != nil {
			return errors.New("属性草稿基值格式无效")
		}
		switch value.(type) {
		case string, float64:
		default:
			return errors.New("属性草稿基值必须是文字或数字")
		}
	}
	return nil
}
func Value(p editor.Project, target, key string) (json.RawMessage, error) {
	if target == "" {
		if key != "projectName" {
			return nil, errors.New("该字段不属于工程名称")
		}
		data, _ := json.Marshal(p.Name)
		return data, nil
	}
	var c *editor.Clip
	for i := range p.Clips {
		if p.Clips[i].ID == target {
			c = &p.Clips[i]
			break
		}
	}
	if c == nil {
		return nil, errors.New("目标片段已不存在")
	}
	var v any
	switch key {
	case "name":
		v = c.Name
	case "text":
		if c.Kind != "text" && c.Kind != "caption" {
			return nil, errors.New("该片段没有文字输入")
		}
		v = c.Text
	case "startFrame":
		v = c.StartFrame
	case "durationFrames":
		v = c.DurationFrames
	case "sourceInUs":
		v = c.SourceInUs
	case "fontSize":
		v = c.TextStyle.FontSize
	case "x":
		v = c.Transform.X
	case "y":
		v = c.Transform.Y
	case "scale":
		v = c.Transform.Scale
	case "rotation":
		v = c.Transform.Rotation
	case "volume":
		v = c.Volume
	case "fadeInUs", "fadeOutUs":
		v = int64(0)
		if c.AudioFade != nil {
			if key == "fadeInUs" {
				v = c.AudioFade.InUs
			} else {
				v = c.AudioFade.OutUs
			}
		}
	case "lutIntensity":
		if c.LUT == nil {
			return nil, errors.New("目标LUT已经移除")
		}
		v = c.LUT.Intensity
	default:
		return nil, errors.New("不支持的属性草稿字段")
	}
	data, err := json.Marshal(v)
	return data, err
}
func Equal(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	switch v := x.(type) {
	case string:
		w, ok := y.(string)
		return ok && v == w
	case float64:
		w, ok := y.(float64)
		return ok && v == w
	}
	return false
}

func rawValue(f Field) (any, error) {
	if f.Key == "projectName" || f.Key == "name" {
		value := strings.TrimSpace(f.Raw)
		if value == "" {
			return nil, errors.New("名称不能为空")
		}
		return value, nil
	}
	if f.Key == "text" {
		return f.Raw, nil
	}
	value := strings.TrimSpace(f.Raw)
	if value == "" {
		return nil, errors.New("数值不能为空")
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, errors.New("请输入有效数字")
	}
	switch f.Key {
	case "scale":
		if n < 0.01 || n > 10 {
			return nil, errors.New("缩放需在0.01至10之间")
		}
	case "rotation":
		if n < -360 || n > 360 {
			return nil, errors.New("旋转需在-360至360之间")
		}
	case "volume", "lutIntensity":
		if n < 0 || n > 100 {
			return nil, errors.New("百分比需在0至100之间")
		}
	case "fontSize":
		if n < 8 || n > 500 {
			return nil, errors.New("字号需在8至500之间")
		}
	}
	switch f.Key {
	case "startFrame", "durationFrames", "fontSize":
		if n != math.Trunc(n) || math.Abs(n) > 9007199254740991 {
			return nil, errors.New("此字段需要安全整数")
		}
		return int64(n), nil
	case "sourceInUs", "fadeInUs", "fadeOutUs":
		// 仅解析有限长度的十进制秒，避免极端指数把任意精度整数扩展到无界内存。
		if len(value) > 64 || strings.ContainsAny(value, "pP/_") {
			return nil, errors.New("时间请使用有界十进制秒数")
		}
		if i := strings.IndexAny(value, "eE"); i >= 0 {
			exponent, err := strconv.Atoi(value[i+1:])
			if err != nil || exponent < -64 || exponent > 64 {
				return nil, errors.New("时间指数超出当前支持范围，请改写十进制秒数")
			}
		}
		r, ok := new(big.Rat).SetString(value)
		if !ok || r.Sign() < 0 {
			return nil, errors.New("时间需为非负秒数")
		}
		r.Mul(r, big.NewRat(1000000, 1))
		r.Add(r, big.NewRat(1, 2))
		q := new(big.Int).Quo(r.Num(), r.Denom())
		if !q.IsInt64() {
			return nil, errors.New("时间超过范围")
		}
		return q.Int64(), nil
	case "volume", "lutIntensity":
		return n / 100, nil
	default:
		return n, nil
	}
}
func Converted(f Field) (json.RawMessage, error) {
	v, err := rawValue(f)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(v)
	return data, err
}

func Preview(p editor.Project, target string, fields []Field, allowConflict bool) ([]Difference, []editor.Command, error) {
	if err := ValidateFields(fields); err != nil {
		return nil, nil, err
	}
	rows := []Difference{}
	valid := true
	var clip editor.Clip
	found := target == ""
	for _, c := range p.Clips {
		if c.ID == target {
			clip = c
			found = true
		}
	}
	if !found {
		return nil, nil, errors.New("目标片段已删除，草稿保留但不自动重建片段")
	}
	var name string
	for _, f := range fields {
		current, err := Value(p, target, f.Key)
		if err != nil {
			return nil, nil, err
		}
		row := Difference{Key: f.Key, Label: labels[f.Key], Raw: f.Raw, Base: f.Base, Current: current, Conflict: !Equal(f.Base, current)}
		v, err := rawValue(f)
		if err != nil {
			row.Error = err.Error()
			valid = false
		} else if row.Conflict && !allowConflict {
			row.Error = "当前属性已变化，请以当前值重新核对"
			valid = false
		} else {
			switch f.Key {
			case "projectName":
				name = v.(string)
			case "name":
				clip.Name = v.(string)
			case "text":
				clip.Text = v.(string)
			case "startFrame":
				clip.StartFrame = v.(int64)
			case "durationFrames":
				clip.DurationFrames = v.(int64)
			case "sourceInUs":
				clip.SourceInUs = v.(int64)
			case "fontSize":
				clip.TextStyle.FontSize = int(v.(int64))
			case "x":
				clip.Transform.X = v.(float64)
			case "y":
				clip.Transform.Y = v.(float64)
			case "scale":
				clip.Transform.Scale = v.(float64)
			case "rotation":
				clip.Transform.Rotation = v.(float64)
			case "volume":
				clip.Volume = v.(float64)
			case "fadeInUs", "fadeOutUs":
				in, out := int64(0), int64(0)
				if clip.AudioFade != nil {
					in, out = clip.AudioFade.InUs, clip.AudioFade.OutUs
				}
				if f.Key == "fadeInUs" {
					in = v.(int64)
				} else {
					out = v.(int64)
				}
				clip.AudioFade = editor.NewAudioFade(clip, in, out)
			case "lutIntensity":
				copy := *clip.LUT
				copy.Intensity = v.(float64)
				clip.LUT = &copy
			}
		}
		rows = append(rows, row)
	}
	if !valid {
		return rows, nil, nil
	}
	if len(fields) == 0 {
		return rows, nil, errors.New("没有可应用的草稿字段")
	}
	if target == "" {
		return rows, []editor.Command{{Type: "update_project", Name: name}}, nil
	}
	for _, track := range p.Tracks {
		if track.ID == clip.TrackID && track.Locked {
			return rows, nil, fmt.Errorf("轨道 %s 已锁定，草稿没有应用", track.Name)
		}
	}
	return rows, []editor.Command{{Type: "update_clip", Clip: &clip}}, nil
}
