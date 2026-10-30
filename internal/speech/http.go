package speech

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)
var sha256Pattern = regexp.MustCompile(`^[A-Fa-f0-9]{64}$`)
var languagePattern = regexp.MustCompile(`^[a-z]{2,3}$`)

type ElevenLabs struct {
	config   Config
	endpoint string
	client   *http.Client
	timeout  time.Duration
}

func NewElevenLabs(config Config) (*ElevenLabs, error) {
	endpoint := config.BaseURL
	if endpoint == "" {
		endpoint = "https://api.elevenlabs.io"
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, failure("endpoint", "语音服务地址无效或包含凭据/查询参数")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	loopback := host == "localhost" || ip != nil && ip.IsLoopback()
	private := host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
	if !(config.AllowLocalTestHTTP && loopback && (u.Scheme == "http" || u.Scheme == "https")) && (u.Scheme != "https" || private) {
		return nil, failure("endpoint", "正式语音连接需要非本地HTTPS地址；loopback仅供显式fixture")
	}
	if config.WorkDir != "" && !filepath.IsAbs(config.WorkDir) {
		return nil, failure("work_directory", "语音临时目录必须为绝对路径")
	}
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 180 * time.Second
	}
	if timeout < time.Second || timeout > 10*time.Minute {
		return nil, failure("timeout", "语音请求超时需在1秒到10分钟之间")
	}
	client := &http.Client{Timeout: timeout}
	if config.HTTPClient != nil {
		copy := *config.HTTPClient
		client = &copy
		if client.Timeout <= 0 {
			client.Timeout = timeout
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ElevenLabs{config: config, endpoint: strings.TrimRight(endpoint, "/"), client: client, timeout: timeout}, nil
}

func newOperation(id, model string) (Operation, error) {
	if id != "" && !safeID.MatchString(id) {
		return Operation{}, failure("request_id", "语音请求标识无效")
	}
	if id == "" {
		var data [16]byte
		if _, err := rand.Read(data[:]); err != nil {
			return Operation{}, failure("request_id", "无法创建语音请求标识")
		}
		id = "speech-" + hex.EncodeToString(data[:])
	}
	return Operation{ID: id, ProviderID: ElevenLabsID, ModelID: model, Status: Failed}, nil
}
func failedOperation(operation Operation, err error) Operation {
	operation.Status = Failed
	var problem *Error
	if errors.As(err, &problem) && problem.SubmissionUncertain {
		operation.Status = Unknown
		operation.SubmissionUncertain = true
	}
	return operation
}

func (p *ElevenLabs) request(ctx context.Context, method, path, contentType string, body io.Reader, size, maximum int64) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if p.config.Secrets == nil {
		return nil, "", failure("credentials", "尚未配置语音服务凭据")
	}
	key, err := p.config.Secrets.Secret(ctx, ElevenLabsID)
	if err != nil || strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
		return nil, "", failure("credentials", "语音服务凭据不可用，请在连接设置中检查")
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint+path, body)
	if err != nil {
		return nil, "", failure("request", "无法创建语音服务请求")
	}
	req.GetBody = nil // 不允许重放可能已经计费的POST。
	if body != nil {
		req.ContentLength = size
	}
	req.Header.Set("xi-api-key", strings.TrimSpace(key))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response, err := p.client.Do(req)
	if err != nil {
		return nil, "", &Error{Code: "transport", Message: "语音连接中断；提交状态可能未知，未自动重试", SubmissionUncertain: method == http.MethodPost, Cause: err}
	}
	defer response.Body.Close()
	remoteID := response.Header.Get("request-id")
	if remoteID == "" {
		remoteID = response.Header.Get("x-request-id")
	}
	if !safeID.MatchString(remoteID) {
		remoteID = ""
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, remoteID, &Error{Code: "provider_http", Message: "语音服务返回错误，请检查权限、配额和参数；未自动重试", HTTPStatus: response.StatusCode, SubmissionUncertain: method == http.MethodPost && (response.StatusCode >= 500 || response.StatusCode == 408 || response.StatusCode == 409 || response.StatusCode >= 300 && response.StatusCode < 400)}
	}
	if response.ContentLength > maximum {
		return nil, remoteID, &Error{Code: "response_limit", Message: "语音响应超过本轮上限，提交结果需核查", SubmissionUncertain: method == http.MethodPost}
	}
	raw, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: response.Body}, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, remoteID, &Error{Code: "response_read", Message: "语音响应读取失败或超过上限，提交结果需核查", SubmissionUncertain: method == http.MethodPost, Cause: err}
	}
	return raw, remoteID, nil
}

type upload struct {
	*os.File
	directory, contentType string
	size                   int64
}

func (u *upload) Close() error { err := u.File.Close(); _ = os.RemoveAll(u.directory); return err }

func (p *ElevenLabs) multipart(ctx context.Context, fields map[string]string, partName string, inputs []*InputFile) (*upload, error) {
	directory, err := os.MkdirTemp(p.config.WorkDir, "aividcut-speech-upload-")
	if err != nil {
		return nil, failure("upload", "无法创建私有语音上传缓冲")
	}
	f, err := os.OpenFile(filepath.Join(directory, "body"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, failure("upload", "无法创建私有语音上传缓冲")
	}
	value := &upload{File: f, directory: directory}
	success := false
	defer func() {
		if !success {
			_ = value.Close()
		}
	}()
	writer := multipart.NewWriter(f)
	value.contentType = writer.FormDataContentType()
	for key, text := range fields {
		if err = writer.WriteField(key, text); err != nil {
			return nil, failure("upload", "无法编码语音上传参数")
		}
	}
	var total int64
	for i, input := range inputs {
		if input == nil || input.Reader == nil || !safeID.MatchString(input.AssetID) || !sha256Pattern.MatchString(input.SHA256) || input.SizeBytes <= 0 || input.SizeBytes > MaxInputBytes || input.DurationUs < 0 || input.Kind != "audio" && input.Kind != "video" || !supportedMIME(input.MIME) {
			return nil, failure("file_scope", "已授权素材读取器没有返回有效媒体快照")
		}
		total += input.SizeBytes
		if total > MaxInputBytes {
			return nil, failure("input_limit", "本轮语音上传总量不能超过64MiB")
		}
		// 不向云端发送原始文件路径或文件名。
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": partName, "filename": "source-" + strconv.Itoa(i) + mediaExtension(input.MIME)}))
		header.Set("Content-Type", input.MIME)
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, failure("upload", "无法创建语音文件上传段")
		}
		hash := sha256.New()
		prefix := &prefixWriter{}
		written, err := io.CopyBuffer(io.MultiWriter(part, hash, prefix), &contextReader{ctx: ctx, reader: io.LimitReader(input.Reader, input.SizeBytes+1)}, make([]byte, 32<<10))
		if err != nil {
			return nil, &Error{Code: "upload", Message: "读取语音上传快照失败或已取消", Cause: err}
		}
		if written != input.SizeBytes {
			return nil, failure("input_changed", "语音快照长度与声明不一致")
		}
		if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), input.SHA256) || mediaMIME(prefix.data, input.Kind) != input.MIME {
			return nil, failure("input_changed", "语音快照指纹或媒体类型与声明不一致")
		}
	}
	if err = writer.Close(); err != nil {
		return nil, failure("upload", "无法完成语音上传编码")
	}
	info, err := f.Stat()
	if err != nil {
		return nil, failure("upload", "无法检查语音上传编码")
	}
	value.size = info.Size()
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, failure("upload", "无法读取语音上传编码")
	}
	success = true
	return value, nil
}

type prefixWriter struct{ data []byte }

func (p *prefixWriter) Write(data []byte) (int, error) {
	count := len(data)
	if len(p.data) < 512 {
		limit := 512 - len(p.data)
		if len(data) > limit {
			data = data[:limit]
		}
		p.data = append(p.data, data...)
	}
	return count, nil
}
func supportedMIME(mime string) bool { return mediaExtension(mime) != "" }
func mediaExtension(mime string) string {
	return map[string]string{"audio/wav": ".wav", "audio/mpeg": ".mp3", "audio/flac": ".flac", "audio/ogg": ".ogg", "video/ogg": ".ogg", "audio/mp4": ".m4a", "video/mp4": ".mp4", "audio/webm": ".webm", "video/webm": ".webm", "video/x-msvideo": ".avi", "audio/aiff": ".aiff"}[mime]
}
