package generation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ImportedAsset struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	MIME     string `json:"mime"`
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
	RecipeID string `json:"recipeId"`
}

type ImportOptions struct {
	Directory          string
	HTTPClient         *http.Client `json:"-"`
	AllowLocalTestHTTP bool         `json:"-"`
	MaxFileBytes       int64
}

type ResultImporter struct {
	directory string
	client    *http.Client
	localTest bool
	limit     int64
}
type importManifest struct {
	TaskID   string          `json:"taskId"`
	RemoteID string          `json:"remoteId,omitempty"`
	Recipe   Recipe          `json:"recipe"`
	Assets   []ImportedAsset `json:"assets"`
}

// RecoverLocal 只核对已原子提交的本地结果；不下载、不查询供应商，也不读取凭据。
func (importer *ResultImporter) RecoverLocal(ctx context.Context, task Task) ([]ImportedAsset, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if task.Status != Succeeded || task.SubmissionUncertain || !safeID.MatchString(task.ID) || task.Recipe.ID != task.ID || task.Recipe.ProviderID != task.ProviderID || task.Recipe.ModelID != task.ModelID {
		return nil, false, failure("task", "任务缺少一致的成功结果身份")
	}
	final := filepath.Join(importer.directory, task.ID)
	if _, err := os.Lstat(final); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, failure("output_scope", "无法检查已有本地生成结果")
	}
	assets, err := importer.existing(task, final)
	if err != nil {
		return nil, true, err
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	return assets, true, nil
}

func NewResultImporter(options ImportOptions) (*ResultImporter, error) {
	if strings.TrimSpace(options.Directory) == "" {
		return nil, failure("output_scope", "需要明确的生成素材保存目录")
	}
	absolute, err := filepath.Abs(options.Directory)
	if err != nil {
		return nil, failure("output_scope", "生成素材目录无效")
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return nil, failure("output_scope", "无法创建生成素材目录")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, failure("output_scope", "无法解析生成素材目录")
	}
	limit := options.MaxFileBytes
	if limit == 0 {
		limit = 512 << 20
	}
	if limit < 1 || limit > 2<<30 {
		return nil, failure("output_limit", "生成结果大小限制无效")
	}
	client := &http.Client{Timeout: 120 * time.Second}
	if options.HTTPClient != nil {
		copy := *options.HTTPClient
		client = &copy
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	standardTransport := client.Transport == nil
	if original, ok := client.Transport.(*http.Transport); ok {
		if original == nil {
			return nil, failure("download_transport", "下载 Transport 不能为 nil 指针")
		}
		transport = original.Clone()
		standardTransport = true
	} else if client.Transport != nil && !options.AllowLocalTestHTTP {
		return nil, failure("download_transport", "正式下载需要可验证的标准 HTTP Transport")
	}
	if standardTransport {
		transport.Proxy = nil
		transport.DialContext = safeDownloadDial
		if options.AllowLocalTestHTTP {
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(address)
				if err == nil {
					ip := net.ParseIP(host)
					if host == "localhost" || (ip != nil && ip.IsLoopback()) {
						return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, address)
					}
				}
				return safeDownloadDial(ctx, network, address)
			}
		}
		client.Transport = transport
	}
	result := &ResultImporter{directory: resolved, client: client, localTest: options.AllowLocalTestHTTP, limit: limit}
	if client.Timeout <= 0 {
		client.Timeout = 120 * time.Second
	}
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return failure("download_redirect", "生成下载重定向过多")
		}
		return result.validateDownloadURL(req.URL.String())
	}
	return result, nil
}

// Import 将一组成功结果作为一个目录原子发布，下载失败不会调用 Submit。
// 同任务已导入时校验配方和文件指纹后返回原结果，避免重复导入或覆盖。
func (importer *ResultImporter) Import(ctx context.Context, task Task) ([]ImportedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if task.Status != Succeeded || task.SubmissionUncertain || !safeID.MatchString(task.ID) || task.Recipe.ID != task.ID || task.Recipe.ProviderID != task.ProviderID || task.Recipe.ModelID != task.ModelID {
		return nil, failure("task", "只有身份和配方一致的成功生成任务可以导入")
	}
	final := filepath.Join(importer.directory, task.ID)
	if _, err := os.Lstat(final); err == nil {
		return importer.existing(task, final)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, failure("output_scope", "无法检查已有生成结果")
	}
	if len(task.Outputs) == 0 || len(task.Outputs) > 10 {
		return nil, failure("result", "生成任务没有结果或结果数量超过限制")
	}
	temporary, err := os.MkdirTemp(importer.directory, ".generation-")
	if err != nil {
		return nil, failure("output_scope", "无法创建生成结果临时目录")
	}
	defer os.RemoveAll(temporary)
	assets := make([]ImportedAsset, 0, len(task.Outputs))
	for index, output := range task.Outputs {
		ext, ok := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "video/mp4": ".mp4"}[output.MIME]
		if !ok || (output.Kind != "image" && output.Kind != "video") || (output.Kind == "image" && !strings.HasPrefix(output.MIME, "image/")) || (output.Kind == "video" && output.MIME != "video/mp4") {
			return nil, failure("result_type", "生成结果的类型或格式尚不支持")
		}
		name := fmt.Sprintf("result-%02d%s", index+1, ext)
		path := filepath.Join(temporary, name)
		if err := importer.writeOutput(ctx, output, path); err != nil {
			return nil, err
		}
		size, hash, err := inspectOutput(path, output.MIME, importer.outputLimit(output.MIME))
		if err != nil {
			return nil, err
		}
		assets = append(assets, ImportedAsset{ID: fmt.Sprintf("%s-%d", task.ID, index+1), Kind: output.Kind, Name: name, Path: filepath.Join(final, name), MIME: output.MIME, SHA256: hash, Bytes: size, RecipeID: task.Recipe.ID})
	}
	manifest := importManifest{TaskID: task.ID, RemoteID: task.RemoteID, Recipe: task.Recipe, Assets: assets}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, failure("recipe", "无法保存生成配方")
	}
	if err := os.WriteFile(filepath.Join(temporary, "recipe.json"), encoded, 0600); err != nil {
		return nil, failure("recipe", "生成配方写入失败")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Rename(temporary, final); err != nil {
		if _, exists := os.Stat(final); exists == nil {
			return importer.existing(task, final)
		}
		return nil, failure("output_scope", "发布生成结果失败，未覆盖原有文件")
	}
	return assets, nil
}

func (importer *ResultImporter) writeOutput(ctx context.Context, output Output, path string) error {
	var reader io.Reader
	var closeBody io.Closer
	if len(output.Data) > 0 {
		if output.URL != "" {
			return failure("result", "结果不能同时包含内联数据和远程地址")
		}
		reader = bytes.NewReader(output.Data)
	} else {
		if err := importer.validateDownloadURL(output.URL); err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, output.URL, nil)
		if err != nil {
			return failure("download", "无法创建生成结果下载")
		}
		// 下载不带生成服务 API Key，不复用原请求鉴权头。
		response, err := importer.client.Do(request)
		if err != nil {
			return failure("download", "生成结果下载失败，可重新查询同一任务后下载；未重新生成")
		}
		closeBody = response.Body
		defer closeBody.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return &Error{Code: "download_http", Message: "生成结果暂时无法下载，请重新查询同一任务后重试下载", HTTPStatus: response.StatusCode}
		}
		reader = response.Body
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return failure("output_scope", "无法写入生成素材")
	}
	limit := importer.outputLimit(output.MIME)
	size, err := io.Copy(file, io.LimitReader(reader, limit+1))
	if err != nil || size == 0 || size > limit {
		file.Close()
		return failure("result_size", "生成结果读取失败、为空或超过文件大小限制")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return failure("output_scope", "生成素材同步失败")
	}
	if err := file.Close(); err != nil {
		return failure("output_scope", "关闭生成素材失败")
	}
	return ctx.Err()
}

func (importer *ResultImporter) outputLimit(mime string) int64 {
	if strings.HasPrefix(mime, "image/") {
		return min(importer.limit, 50<<20)
	}
	return importer.limit
}

func inspectOutput(path, mime string, limit int64) (int64, string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return 0, "", failure("result_size", "生成结果文件缺失或超出限制")
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, "", failure("result", "无法验证生成结果")
	}
	defer file.Close()
	header := make([]byte, 512)
	n, _ := file.Read(header)
	header = header[:n]
	if http.DetectContentType(header) != mime {
		return 0, "", failure("result_type", "生成文件内容与声明格式不一致")
	}
	if mime == "image/png" || mime == "image/jpeg" {
		file.Seek(0, io.SeekStart)
		config, _, err := image.DecodeConfig(file)
		if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 16_777_216 {
			return 0, "", failure("result_image", "生成图片尺寸无效或超过安全解码限制")
		}
		file.Seek(0, io.SeekStart)
		if _, _, err := image.Decode(file); err != nil {
			return 0, "", failure("result_image", "生成图片数据不完整")
		}
	} else if mime == "image/webp" {
		if len(header) < 20 || string(header[8:12]) != "WEBP" {
			return 0, "", failure("result_image", "生成 WebP 容器不完整")
		}
	}
	file.Seek(0, io.SeekStart)
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return 0, "", failure("result", "生成文件指纹计算失败")
	}
	return info.Size(), hex.EncodeToString(hash.Sum(nil)), nil
}

func (importer *ResultImporter) existing(task Task, folder string) ([]ImportedAsset, error) {
	info, err := os.Lstat(folder)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, failure("output_scope", "已有结果路径不是安全目录")
	}
	recipePath := filepath.Join(folder, "recipe.json")
	recipeInfo, err := os.Lstat(recipePath)
	if err != nil || !recipeInfo.Mode().IsRegular() {
		return nil, failure("recipe_conflict", "已有配方文件不安全或缺失")
	}
	file, err := os.Open(recipePath)
	if err != nil {
		return nil, failure("recipe_conflict", "已有结果缺少可核查配方，不覆盖")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20))
	if err != nil {
		return nil, failure("recipe", "无法读取已有配方")
	}
	var manifest importManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, failure("recipe", "已有生成配方损坏")
	}
	expected, _ := json.Marshal(task.Recipe)
	actual, _ := json.Marshal(manifest.Recipe)
	if manifest.TaskID != task.ID || manifest.RemoteID != task.RemoteID || !bytes.Equal(expected, actual) || len(manifest.Assets) == 0 || len(manifest.Assets) > 10 {
		return nil, failure("recipe_conflict", "相同结果目录对应不同生成配方，不覆盖")
	}
	for _, asset := range manifest.Assets {
		resolved, resolveErr := filepath.EvalSymlinks(asset.Path)
		if resolveErr != nil {
			return nil, failure("result_changed", "已有生成素材缺失或链接已变化")
		}
		relative, err := filepath.Rel(folder, resolved)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, failure("output_scope", "已有配方引用了目录外文件")
		}
		if asset.RecipeID != task.Recipe.ID {
			return nil, failure("recipe_conflict", "生成素材与配方关联不一致")
		}
		size, hash, err := inspectOutput(asset.Path, asset.MIME, importer.outputLimit(asset.MIME))
		if err != nil || size != asset.Bytes || hash != asset.SHA256 {
			return nil, failure("result_changed", "已有生成素材缺失或已变化，请重新查询任务或修复文件")
		}
	}
	return manifest.Assets, nil
}

func (importer *ResultImporter) validateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if len(raw) > 16384 || err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return failure("download_scope", "生成下载地址无效")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if importer.localTest && (host == "localhost" || (ip != nil && ip.IsLoopback())) && (u.Scheme == "http" || u.Scheme == "https") {
		return nil
	}
	if u.Scheme != "https" || host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return failure("download_scope", "生成结果只允许非本地 HTTPS 下载地址")
	}
	if ip != nil && !publicIP(ip) {
		return failure("download_scope", "禁止从本地、私有或保留地址下载生成结果")
	}
	return nil
}

func publicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32", "fec0::/10"} {
		if netip.MustParsePrefix(prefix).Contains(address) {
			return false
		}
	}
	return true
}

// 默认下载器固定解析后的公网 IP，避免检查域名后由第二次解析绕回私网。
func safeDownloadDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, failure("download_scope", "下载地址端口无效")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, failure("download", "无法解析生成下载地址")
	}
	for _, ip := range ips {
		if !publicIP(ip.IP) {
			return nil, failure("download_scope", "生成下载域名解析到了非公网地址")
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	for _, ip := range ips {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return connection, nil
		}
	}
	return nil, failure("download", "无法连接生成下载服务")
}
