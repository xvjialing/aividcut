package playback

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestBinaryFrameRoundTripAndBoundedPayload(t *testing.T) {
	frame := Frame{Position: 3, Width: 16, Height: 16, FPSNum: 30000, FPSDen: 1001, SampleRate: 48000, Channels: 2, Samples: 1602, AudioStartSample: 4805, RGBA: make([]byte, 16*16*4), PCM: make([]byte, 1602*8)}
	for i := 0; i < len(frame.RGBA); i += 4 {
		frame.RGBA[i], frame.RGBA[i+3] = 255, 255
	}
	binary.LittleEndian.PutUint32(frame.PCM[20:], math.Float32bits(.25))
	data, err := EncodeFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := readPacket(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Position != 3 || decoded.AudioStartSample != 4805 || !bytes.Equal(decoded.RGBA, frame.RGBA) || !bytes.Equal(decoded.PCM, frame.PCM) {
		t.Fatal("二进制帧、PCM或真实采样anchor变化")
	}
	for _, test := range []struct {
		name   string
		mutate func([]byte)
	}{{"错误magic", func(b []byte) { b[0] = 'X' }}, {"元信息过大", func(b []byte) { binary.BigEndian.PutUint32(b[4:], MaxHeaderBytes+1) }}, {"图过大", func(b []byte) { binary.BigEndian.PutUint32(b[8:], MaxImageBytes+1) }}, {"PCM过大", func(b []byte) { binary.BigEndian.PutUint32(b[12:], MaxAudioBytes+1) }}} {
		t.Run(test.name, func(t *testing.T) {
			broken := bytes.Clone(data)
			test.mutate(broken)
			if _, err := readPacket(bytes.NewReader(broken)); err == nil {
				t.Fatal("越界包未在分配大内存前拒绝")
			}
		})
	}
	frame.PCM = frame.PCM[:8]
	if data, err := EncodeFrame(frame); err == nil {
		if _, err := readPacket(bytes.NewReader(data)); err == nil {
			t.Fatal("samples与PCM字节数不一致仍通过")
		}
	}
}

func TestNonFinitePCMIsRejected(t *testing.T) {
	frame := Frame{Width: 16, Height: 16, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, Samples: 1, RGBA: make([]byte, 16*16*4), PCM: make([]byte, 8)}
	binary.LittleEndian.PutUint32(frame.PCM, math.Float32bits(float32(math.NaN())))
	data, _ := EncodeFrame(frame)
	if _, err := readPacket(bytes.NewReader(data)); err == nil {
		t.Fatal("NaN不能发送到AudioWorklet")
	}
}
