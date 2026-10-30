// Package previewproxy 定义可重建的画面代理证据，不进入工程格式。
package previewproxy

import (
	"errors"
	"regexp"
)

const Version = "video-proxy-v1"
const MaxFileBytes int64 = 2 << 30
const MaxCacheBytes int64 = 8 << 30
const MaxEntries = 32

var fingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var keyPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ValidKey(key string) bool { return keyPattern.MatchString(key) }
func Edge(profile string) (int, error) {
	switch profile {
	case "720p":
		return 1280, nil
	case "540p":
		return 960, nil
	default:
		return 0, errors.New("代理仅支持720p或540p长边档位")
	}
}

type Proof struct {
	OriginUs         int64  `json:"originUs"`
	Frames           int64  `json:"frames"`
	TimelineSHA256   string `json:"timelineSha256"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	ProxyFingerprint string `json:"proxyFingerprint"`
	Bytes            int64  `json:"bytes"`
}
type Manifest struct {
	Version           string `json:"version"`
	Key               string `json:"key"`
	SourceFingerprint string `json:"sourceFingerprint"`
	Profile           string `json:"profile"`
	Proof             Proof  `json:"proof"`
}

func (m Manifest) Validate() error {
	edge, err := Edge(m.Profile)
	if err != nil {
		return err
	}
	if m.Version != Version || !ValidKey(m.Key) || !ValidKey(m.Proof.TimelineSHA256) || !fingerprintPattern.MatchString(m.SourceFingerprint) || !fingerprintPattern.MatchString(m.Proof.ProxyFingerprint) || m.Proof.Frames < 1 || m.Proof.Frames > 1_000_000 || m.Proof.Width < 2 || m.Proof.Height < 2 || m.Proof.Width > edge || m.Proof.Height > edge || m.Proof.Bytes < 1 || m.Proof.Bytes > MaxFileBytes || m.Proof.OriginUs < -86_400_000_000 || m.Proof.OriginUs > 86_400_000_000 {
		return errors.New("代理证据不完整或超出预算")
	}
	return nil
}
