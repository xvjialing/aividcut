package playback

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestOfflinePacketDoesNotExpandWebViewBoundary(t *testing.T) {
	frame := Frame{Width: 1922, Height: 16, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, Samples: 1600, RGBA: make([]byte, 1922*16*4), PCM: make([]byte, 1600*8)}
	value := packet{Frame: frame, Type: "frame"}
	header, _ := json.Marshal(value)
	wire := make([]byte, 16)
	copy(wire, Protocol)
	binary.BigEndian.PutUint32(wire[4:], uint32(len(header)))
	binary.BigEndian.PutUint32(wire[8:], uint32(len(frame.RGBA)))
	binary.BigEndian.PutUint32(wire[12:], uint32(len(frame.PCM)))
	wire = append(wire, header...)
	wire = append(wire, frame.RGBA...)
	wire = append(wire, frame.PCM...)
	if _, err := readPacket(bytes.NewReader(wire)); err == nil {
		t.Fatal("预览接受超宽帧")
	}
	var image, audio []byte
	got, err := readPacketMode(bytes.NewReader(wire), &image, &audio, true)
	if err != nil || got.Width != 1922 {
		t.Fatal(got.Width, err)
	}
	if _, err := EncodeFrame(got.Frame); err == nil {
		t.Fatal("离线帧进入WebView通道")
	}
	binary.BigEndian.PutUint32(wire[8:], MaxOfflineImageBytes+1)
	if _, err := readPacketMode(bytes.NewReader(wire), &image, &audio, true); err == nil {
		t.Fatal("离线协议超过原画幅字节预算")
	}
}
