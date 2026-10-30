package media

type WaveformReader = sourceReader[WaveformTile]

func NewWaveformReader(tools Toolchain) *WaveformReader {
	return newSourceReader(tools.ExtractWaveformTile, func(tile WaveformTile) WaveformTile {
		tile.Peaks = append([]WaveformPeak{}, tile.Peaks...)
		return tile
	}, "波形")
}
