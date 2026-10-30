// Package ocr 隔离平台识别器；只接收应用核对后的JPEG，不拥有工程写入权限。
package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"aividcut/internal/managedprocess"
)

type Capability struct {
	Available bool   `json:"available"`
	Engine    string `json:"engine"`
	Reason    string `json:"reason"`
}
type Line struct {
	Text       string   `json:"text"`
	Confidence *float64 `json:"confidence"`
}
type Result struct {
	Version   int      `json:"version"`
	Engine    string   `json:"engine"`
	Revision  int      `json:"revision"`
	Languages []string `json:"languages"`
	Lines     []Line   `json:"lines"`
	Truncated bool     `json:"truncated"`
}
type Recognizer interface {
	Recognize(context.Context, []byte) (Result, error)
}
type Worker struct {
	Path   string
	Engine string
}

func Discover() (Capability, Worker) {
	executable, _ := os.Executable()
	return discoverFor(runtime.GOOS, executable, strings.TrimSpace(os.Getenv("AIVIDCUT_OCR_WORKER")), packageIdentityAvailable())
}
func discoverFor(platform, executable, override string, packaged bool) (Capability, Worker) {
	c := Capability{}
	name := ""
	switch platform {
	case "darwin":
		c.Engine = "apple-vision"
		name = "AIVidCutOCR"
	case "windows":
		c.Engine = "windows-media-ocr"
		name = "AIVidCutOCR.exe"
		if !packaged {
			c.Reason = "Windows系统OCR需要MSIX包身份，当前便携运行方式不可用"
			return c, Worker{}
		}
	default:
		c.Reason = "当前平台尚未配置本地文字识别适配器"
		return c, Worker{}
	}
	path := override
	if path == "" && executable != "" {
		path = filepath.Join(filepath.Dir(executable), name)
	}
	info, err := os.Lstat(path)
	if err != nil || !filepath.IsAbs(path) || !info.Mode().IsRegular() || platform != "windows" && info.Mode()&0111 == 0 {
		c.Reason = "未找到随包本地文字识别进程，请使用完整客户端构建"
		return c, Worker{}
	}
	c.Available = true
	if platform == "windows" {
		c.Reason = "本机Windows系统OCR；实际语言以系统已安装内容为准"
	} else {
		c.Reason = "本机Apple Vision，识别当前取样帧的中英文，不上传素材"
	}
	return c, Worker{Path: path, Engine: c.Engine}
}

type boundedBuffer struct {
	bytes.Buffer
	remaining int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.remaining {
		return 0, errors.New("识别进程输出超过预算")
	}
	b.remaining -= len(p)
	return b.Buffer.Write(p)
}
func (w Worker) Recognize(parent context.Context, jpeg []byte) (Result, error) {
	if len(jpeg) == 0 || len(jpeg) > 4<<20 || !bytes.HasPrefix(jpeg, []byte{0xff, 0xd8}) {
		return Result{}, errors.New("识别输入必须为4MiB以内的源JPEG")
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	command := managedprocess.CommandContext(ctx, w.Path)
	configureWorker(command)
	command.Stdin = bytes.NewReader(jpeg)
	out, stderr := &boundedBuffer{remaining: 256 << 10}, &boundedBuffer{remaining: 16 << 10}
	command.Stdout = out
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if message := workerError(out.Bytes()); message != "" {
			return Result{}, errors.New(message)
		}
		return Result{}, errors.New("本地文字识别进程失败，请核对系统和完整构建")
	}
	var result Result
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Result{}, errors.New("识别进程返回格式无效")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Result{}, errors.New("识别进程返回多余数据")
	}
	expected := w.Engine
	if expected == "" {
		expected = "apple-vision"
	}
	if result.Engine != expected {
		return Result{}, errors.New("识别进程返回其他引擎身份")
	}
	if err := Validate(result); err != nil {
		return Result{}, err
	}
	return result, nil
}
func workerError(data []byte) string {
	if len(data) > 256 {
		return ""
	}
	var value struct {
		Code string `json:"errorCode"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return ""
	}
	switch value.Code {
	case "package_identity_required":
		return "Windows系统OCR需要MSIX包身份，请使用已安装的身份包启动"
	case "no_language":
		return "系统没有可用OCR语言，请在Windows语言设置中安装所需识别语言后重试"
	case "invalid_request":
		return "识别进程拒绝了无效或过大的源画面"
	case "recognition_failed":
		return "Windows系统文字识别失败，请核对系统组件和语言"
	}
	return ""
}
func SourceIdentity(engine string) (string, string, error) {
	switch engine {
	case "apple-vision":
		return "apple-vision", "apple-vision-ocr-v1", nil
	case "windows-media-ocr":
		return "windows-media-ocr", "windows-media-ocr-v1", nil
	default:
		return "", "", errors.New("未知本地识别器")
	}
}

var languageTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,4}$`)

func Validate(result Result) error {
	if result.Version != 1 || len(result.Lines) > 50 || len(result.Languages) < 1 || len(result.Languages) > 3 {
		return errors.New("识别器身份或数量无效")
	}
	if _, _, err := SourceIdentity(result.Engine); err != nil {
		return err
	}
	if result.Engine == "apple-vision" && result.Revision != 3 || result.Engine == "windows-media-ocr" && (result.Revision != 0 || len(result.Languages) != 1) {
		return errors.New("识别器修订或语言数量无效")
	}
	seen := map[string]bool{}
	for _, language := range result.Languages {
		valid := languageTag.MatchString(language)
		if result.Engine == "apple-vision" {
			valid = language == "zh-Hans" || language == "zh-Hant" || language == "en-US"
		}
		if !valid || seen[language] {
			return errors.New("识别器语言无效或重复")
		}
		seen[language] = true
	}
	for _, line := range result.Lines {
		if !utf8.ValidString(line.Text) || strings.TrimSpace(line.Text) == "" || utf8.RuneCountInString(line.Text) > 1000 || strings.ContainsRune(line.Text, 0) {
			return errors.New("识别文字无效")
		}
		if result.Engine == "apple-vision" && line.Confidence == nil || result.Engine == "windows-media-ocr" && line.Confidence != nil {
			return errors.New("识别器置信度来源不符")
		}
		if line.Confidence != nil {
			v := *line.Confidence
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
				return errors.New("识别置信度无效")
			}
		}
	}
	return nil
}
