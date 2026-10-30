package playback

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"aividcut/internal/editor"
)

func contains(values []string, name string) bool {
	for _, value := range values {
		if value == name {
			return true
		}
	}
	return false
}
func escaped(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}
func prop(name, value string) string {
	return `<property name="` + escaped(name) + `">` + escaped(value) + `</property>`
}
func decimal(value float64) string { return strconv.FormatFloat(value, 'f', 6, 64) }

// ValidateProject与Load使用相同编译器；缺少插件或未验证语义均显式拒绝。
func ValidateProject(project editor.Project, capabilities Capabilities) error {
	if len(project.Clips) > 128 && capabilities.WindowedGraph.Supported {
		_, err := prepareGraphPlan(project, capabilities, 0, 0)
		return err
	}
	_, _, err := compileProject(project, capabilities)
	return err
}
func compileProject(project editor.Project, capabilities Capabilities) (string, int64, error) {
	if err := editor.ValidateProject(project); err != nil {
		return "", 0, err
	}
	return compileSelection(project, project.Clips, capabilities, 0, false)
}

// selection只用于已完整验证的不可变工程；保留全工程坐标和媒体配方。
func compileSelection(project editor.Project, selection []editor.Clip, capabilities Capabilities, fullDuration int64, validationOnly bool) (string, int64, error) {
	return compileScaledSelection(project, selection, capabilities, fullDuration, validationOnly, 1)
}
func compileScaledSelection(project editor.Project, selection []editor.Clip, capabilities Capabilities, fullDuration int64, validationOnly bool, layoutScale float64, sources ...map[string]VisualSource) (string, int64, error) {
	if !capabilities.Available {
		return "", 0, errors.New("当前MLT插件不足以建立原生音视频图")
	}
	settings := project.Settings
	if settings.Width < 16 || settings.Height < 16 || int64(settings.Width)*int64(settings.Height) > 3840*2160 || settings.FPSNum < 1 || settings.FPSDen < 1 || float64(settings.FPSNum)/float64(settings.FPSDen) < 2 || float64(settings.FPSNum)/float64(settings.FPSDen) > 120 || settings.SampleRate < 8000 || settings.SampleRate > 192000 {
		return "", 0, errors.New("当前原生预览限制为最多4K像素、2–120fps、8–192kHz")
	}
	if len(selection) > 128 {
		return "", 0, errors.New("当前原生图最多128个片段；尚未完成大工程解码器资源验收")
	}
	assets := map[string]editor.Asset{}
	for _, asset := range project.Assets {
		assets[asset.ID] = asset
	}
	tracks := map[string]editor.Track{}
	order := map[string]int{}
	for i, track := range project.Tracks {
		tracks[track.ID] = track
		order[track.ID] = i
	}
	clips := append([]editor.Clip{}, selection...)
	sort.SliceStable(clips, func(i, j int) bool {
		if order[clips[i].TrackID] != order[clips[j].TrackID] {
			return order[clips[i].TrackID] > order[clips[j].TrackID]
		}
		return clips[i].StartFrame < clips[j].StartFrame
	})
	duration := fullDuration
	for _, clip := range clips {
		duration = max(duration, clip.StartFrame+clip.DurationFrames)
	}
	if duration < 1 || float64(duration)*float64(settings.FPSDen)/float64(settings.FPSNum) > 86400 {
		return "", 0, errors.New("原生预览需要非空且不超过24小时的时间线")
	}
	var producers, playlists, trackXML, transitions strings.Builder
	producers.WriteString(boundedProducer("base", duration) + prop("mlt_service", "colour") + prop("resource", "black") + `</producer>`)
	trackXML.WriteString(`<track producer="base"/>`)
	index := 0
	videoSources := map[videoSourceKey]string{}
	for _, clip := range clips {
		track := tracks[clip.TrackID]
		if clip.SpeedNum != clip.SpeedDen && (!capabilities.Speed.Supported || float64(clip.SpeedNum)/float64(clip.SpeedDen) < .125 || float64(clip.SpeedNum)/float64(clip.SpeedDen) > 8) {
			return "", 0, errors.New("当前worker缺少已核查的源时钟/保音高变速，或速度超出0.125–8倍")
		}
		if clip.Kind == "text" || clip.Kind == "caption" {
			if utf8.RuneCountInString(clip.Text) > 10000 {
				return "", 0, errors.New("当前原生文字单片段不超过10000字符")
			}
			if clip.Transform.Rotation != 0 || clip.LUT != nil {
				return "", 0, errors.New("文字/字幕旋转及LUT与当前导出语义不一致，请先移除")
			}
			if !capabilities.Text.Supported || !contains(capabilities.Filters, "qtext") {
				return "", 0, errors.New("MLT缺少已配置的qtext文字/字幕插件")
			}
			index++
			id, playlist := fmt.Sprintf("clip%d", index), fmt.Sprintf("playlist%d", index)
			pixels, err := scaledTextFontPixels(clip, layoutScale)
			if err != nil {
				return "", 0, err
			}
			family, style, weight := "Sans", "normal", 400
			if clip.TextStyle.FontAssetID != "" {
				if !capabilities.FontFiles.Supported {
					return "", 0, errors.New("当前worker不支持工程字体，请更新原生媒体工具")
				}
				font := assets[clip.TextStyle.FontAssetID]
				if font.Font == nil {
					return "", 0, errors.New("工程字体资源缺少元信息")
				}
				family, style, weight = font.Font.Family, font.Font.Style, font.Font.Weight
			}
			foreground, err := mltColor(clip.TextStyle.Color, "0xffffffff")
			if err != nil {
				return "", 0, err
			}
			background, err := mltColor(clip.TextStyle.Background, "0x00000000")
			if err != nil {
				return "", 0, err
			}
			align := clip.TextStyle.Align
			if align == "" {
				align = "center"
			}
			if align != "left" && align != "center" && align != "right" {
				return "", 0, errors.New("未支持的原生文字对齐方式")
			}
			y, valign := clip.Transform.Y, "middle"
			if clip.Kind == "caption" {
				y -= 48 * layoutScale
				valign = "bottom"
			}
			geometry := fmt.Sprintf("%s/%s:%dx%d:100", decimal(32*layoutScale+clip.Transform.X), decimal(y), settings.Width-int(math.Round(64*layoutScale)), settings.Height)
			// qtext的argument走QPainterPath纯字形；html/resource非空才会进入QTextDocument。
			producers.WriteString(boundedProducer(id, clip.DurationFrames) + prop("mlt_service", "colour") + prop("resource", "0x00000000") + `<filter>` + prop("mlt_service", "qtext") + prop("html", "") + prop("resource", "") + prop("argument", clip.Text) + prop("geometry", geometry) + prop("size", strconv.Itoa(pixels)) + prop("family", family) + prop("style", style) + prop("weight", strconv.Itoa(weight)) + prop("fgcolour", foreground) + prop("bgcolour", background) + prop("olcolour", "0x00000000") + prop("outline", "0") + prop("halign", align) + prop("valign", valign) + `</filter></producer>`)
			playlists.WriteString(`<playlist id="` + playlist + `">`)
			if clip.StartFrame > 0 {
				playlists.WriteString(`<blank length="` + strconv.FormatInt(clip.StartFrame, 10) + `"/>`)
			}
			playlists.WriteString(fmt.Sprintf(`<entry producer="%s" in="0" out="%d"/></playlist>`, id, clip.DurationFrames-1))
			trackXML.WriteString(`<track producer="` + playlist + `" hide="audio"/>`)
			if !track.Hidden {
				transition, err := compilePreparedVisualTransition(settings, capabilities, index, duration)
				if err != nil {
					return "", 0, err
				}
				transitions.WriteString(transition)
			}
			continue
		}
		asset, exists := assets[clip.AssetID]
		if !exists || asset.Missing || !filepath.IsAbs(asset.Path) || strings.ContainsAny(asset.Path, "\x00\r\n?") {
			return "", 0, errors.New("原生媒体必须是当前工程可用的本地普通素材")
		}
		stat, err := os.Stat(asset.Path)
		if err != nil || !stat.Mode().IsRegular() {
			return "", 0, errors.New("原生素材已缺失或不是普通文件")
		}
		ext := strings.ToLower(filepath.Ext(asset.Path))
		allowed := map[string]bool{".mp4": true, ".mov": true, ".mkv": true, ".webm": true, ".avi": true, ".m4v": true, ".wav": true, ".mp3": true, ".m4a": true, ".aac": true, ".flac": true, ".ogg": true, ".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".bmp": true}
		if !allowed[ext] {
			return "", 0, errors.New("原生路径格式尚未核查，不交给MLT自动选择任意服务")
		}
		var visual *VisualSource
		if len(sources) > 0 && clip.Kind == "video" {
			if candidate, ok := sources[0][asset.ID]; ok {
				visual = &candidate
			}
		}
		parts, err := compileSourceClip(settings, clip, asset, assets, track, capabilities, index, duration, videoSources, visual)
		if err != nil {
			return "", 0, err
		}
		producers.WriteString(parts.producers)
		playlists.WriteString(parts.playlists)
		trackXML.WriteString(parts.tracks)
		transitions.WriteString(parts.transitions)
		index = parts.index
	}
	xml := fmt.Sprintf(`<mlt producer="main"><profile width="%d" height="%d" frame_rate_num="%d" frame_rate_den="%d" progressive="1" sample_aspect_num="1" sample_aspect_den="1" display_aspect_num="%d" display_aspect_den="%d" colorspace="709"/>`, settings.Width, settings.Height, settings.FPSNum, settings.FPSDen, settings.Width, settings.Height) + producers.String() + playlists.String() + `<tractor id="main" in="0" out="` + strconv.FormatInt(duration-1, 10) + `">` + trackXML.String() + transitions.String() + `</tractor></mlt>`
	if !validationOnly && len(xml) > 3<<20 {
		return "", 0, errors.New("原生工程图超过3MiB限制")
	}
	return xml, duration, nil
}

func mltColor(value, fallback string) (string, error) {
	if value == "" || value == "transparent" {
		return fallback, nil
	}
	if (len(value) != 7 && len(value) != 9) || value[0] != '#' {
		return "", errors.New("原生文字颜色需#RRGGBB或#RRGGBBAA")
	}
	for _, digit := range value[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", digit) {
			return "", errors.New("原生文字颜色无效")
		}
	}
	if len(value) == 7 {
		value += "ff"
	}
	return "0x" + value[1:], nil
}

func textFontPixels(clip editor.Clip) (int, error) {
	size := clip.TextStyle.FontSize
	if size == 0 {
		size = 48
	}
	scale := clip.Transform.Scale
	if scale == 0 {
		scale = 1
	}
	text, _, _ := strings.Cut(decimal(float64(size)*scale), ".")
	pixels, err := strconv.Atoi(text)
	if err != nil || pixels < 1 || pixels > 1000 {
		return 0, errors.New("当前实验原生字体大小限制为1–1000实际像素")
	}
	return pixels, nil
}
