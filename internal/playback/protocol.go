package playback

import (
	"aividcut/internal/editor"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
)

const MaxHeaderBytes = 128 << 10
const MaxImageBytes = 1920 * 1080 * 4
const MaxOfflineImageBytes = 3840 * 2160 * 4
const MaxAudioBytes = 1 << 20
const maxCommandBytes = 4 << 20

type packet struct {
	Font *editor.FontInfo `json:"font,omitempty"`
	Frame
	RequestID    uint64           `json:"requestId"`
	Type         string           `json:"type"`
	Error        string           `json:"error,omitempty"`
	Info         *Info            `json:"info,omitempty"`
	Capabilities *Capabilities    `json:"capabilities,omitempty"`
	Measurement  *textMeasurement `json:"measurement,omitempty"`
	Diagnostics  *Diagnostics     `json:"diagnostics,omitempty"`
}

type textMeasurement struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func readPacket(reader io.Reader) (packet, error) {
	var imageBuffer, audioBuffer []byte
	return readPacketInto(reader, &imageBuffer, &audioBuffer)
}

func readPacketInto(reader io.Reader, imageBuffer, audioBuffer *[]byte) (packet, error) {
	return readPacketMode(reader, imageBuffer, audioBuffer, false)
}
func readPacketMode(reader io.Reader, imageBuffer, audioBuffer *[]byte, offline bool) (packet, error) {
	var value packet
	maxImage, maxWidth, maxHeight := MaxImageBytes, 1920, 1080
	if offline {
		maxImage, maxWidth, maxHeight = MaxOfflineImageBytes, 8192, 8192
	}
	var header [16]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return value, err
	}
	if string(header[:4]) != Protocol {
		return value, errors.New("原生播放协议标识不匹配")
	}
	sizes := [3]uint32{binary.BigEndian.Uint32(header[4:8]), binary.BigEndian.Uint32(header[8:12]), binary.BigEndian.Uint32(header[12:16])}
	if sizes[0] == 0 || sizes[0] > MaxHeaderBytes || int64(sizes[1]) > int64(maxImage) || sizes[2] > MaxAudioBytes {
		return value, errors.New("原生播放数据超过有界协议上限")
	}
	metadata := make([]byte, sizes[0])
	if _, err := io.ReadFull(reader, metadata); err != nil {
		return value, err
	}
	if err := json.Unmarshal(metadata, &value); err != nil {
		return value, errors.New("原生播放元数据无效")
	}
	if value.Type == "frame" {
		if value.Width < 1 || value.Height < 1 || value.Width > maxWidth || value.Height > maxHeight || int64(value.Width)*int64(value.Height)*4 != int64(sizes[1]) || value.SampleRate < 8000 || value.SampleRate > 192000 || value.Channels != 2 || value.Samples < 0 || int64(value.Samples)*8 != int64(sizes[2]) || value.Position < 0 || value.AudioStartSample < 0 || value.FPSNum < 1 || value.FPSDen < 1 {
			return value, errors.New("原生帧尺寸、PCM或时钟不一致")
		}
	} else if sizes[1] != 0 || sizes[2] != 0 {
		return value, errors.New("非帧响应夹带二进制数据")
	}
	resize := func(buffer *[]byte, size uint32) []byte {
		if cap(*buffer) < int(size) {
			*buffer = make([]byte, size)
		} else {
			*buffer = (*buffer)[:size]
		}
		return *buffer
	}
	value.RGBA = resize(imageBuffer, sizes[1])
	value.PCM = resize(audioBuffer, sizes[2])
	if _, err := io.ReadFull(reader, value.RGBA); err != nil {
		return value, err
	}
	if _, err := io.ReadFull(reader, value.PCM); err != nil {
		return value, err
	}
	for i := 0; i < len(value.PCM); i += 4 {
		v := math.Float32frombits(binary.LittleEndian.Uint32(value.PCM[i:]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return value, errors.New("原生PCM包含非有限值")
		}
	}
	return value, nil
}

// EncodeFrame供应用WebSocket复用同一有界格式；音视频字节不进入JSON。
func EncodeFrame(frame Frame) ([]byte, error) {
	if err := validateFrame(frame); err != nil {
		return nil, err
	}
	value := packet{Frame: frame, Type: "frame"}
	metadata, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(metadata) > MaxHeaderBytes || len(frame.RGBA) > MaxImageBytes || len(frame.PCM) > MaxAudioBytes {
		return nil, errors.New("播放帧超过传输上限")
	}
	result := make([]byte, 16+len(metadata)+len(frame.RGBA)+len(frame.PCM))
	copy(result, Protocol)
	binary.BigEndian.PutUint32(result[4:8], uint32(len(metadata)))
	binary.BigEndian.PutUint32(result[8:12], uint32(len(frame.RGBA)))
	binary.BigEndian.PutUint32(result[12:16], uint32(len(frame.PCM)))
	copy(result[16:], metadata)
	copy(result[16+len(metadata):], frame.RGBA)
	copy(result[16+len(metadata)+len(frame.RGBA):], frame.PCM)
	return result, nil
}

// WriteFrame允许WebSocket二进制writer直接写帧，避免再复制一整份RGBA包。
func WriteFrame(writer io.Writer, frame Frame) error {
	if err := validateFrame(frame); err != nil {
		return err
	}
	metadata, err := json.Marshal(packet{Frame: frame, Type: "frame"})
	if err != nil {
		return err
	}
	var prefix [16]byte
	copy(prefix[:], Protocol)
	binary.BigEndian.PutUint32(prefix[4:8], uint32(len(metadata)))
	binary.BigEndian.PutUint32(prefix[8:12], uint32(len(frame.RGBA)))
	binary.BigEndian.PutUint32(prefix[12:16], uint32(len(frame.PCM)))
	for _, data := range [][]byte{prefix[:], metadata, frame.RGBA, frame.PCM} {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n != len(data) {
			return io.ErrShortWrite
		}
	}
	return nil
}

func validateFrame(frame Frame) error {
	if frame.Width < 1 || frame.Width > 1920 || frame.Height < 1 || frame.Height > 1080 || len(frame.RGBA) != frame.Width*frame.Height*4 || len(frame.RGBA) > MaxImageBytes || len(frame.PCM) > MaxAudioBytes || frame.Samples < 1 || frame.Samples > MaxAudioBytes/8 || len(frame.PCM) != frame.Samples*8 || frame.Channels != 2 || frame.SampleRate != 48000 || frame.Position < 0 || frame.AudioStartSample < 0 || frame.FPSNum < 1 || frame.FPSDen < 1 {
		return errors.New("原生帧或PCM时钟不符合输出契约")
	}
	return nil
}
