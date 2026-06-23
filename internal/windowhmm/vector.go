// Package windowhmm provides the block-grid types and window-vector extraction
// used by the sliding-window beam-search decoder in mosaictext.
//
// Only [BlockCell] and [WindowVector] are kept here; the trained-HMM machinery
// (KMeans, Train, Model.Viterbi, Concatenate) has been removed because
// DecodeWindowHMM runs a character-level beam search, not a corpus-trained HMM.
package windowhmm

// BlockCell holds the mean RGB of one pixelated block, with values in [0, 255].
// It mirrors internal/refmatch.BlockSig but is defined here to keep
// internal/windowhmm free of cross-internal imports.
type BlockCell struct {
	R, G, B float64
}

// WindowVector flattens a horizontal window of block columns [colStart,
// colStart+w) from a [R][C] block grid into a []float64 normalised to [0, 1].
//
// The grid is indexed [row][col]; each block carries three float64 channels
// (R, G, B) in [0, 255]. The returned vector has length R·w·3. Channel values
// are divided by 255 so the beam-search MSE metric is scale-invariant.
//
// It returns nil when the window extends beyond the grid width or when R or w
// is zero.
func WindowVector(grid [][]BlockCell, colStart, w int) []float64 {
	if len(grid) == 0 || w <= 0 {
		return nil
	}
	nCols := len(grid[0])
	if colStart < 0 || colStart+w > nCols {
		return nil
	}

	nRows := len(grid)
	v := make([]float64, nRows*w*3)
	idx := 0
	for r := range nRows {
		for c := colStart; c < colStart+w; c++ {
			cell := grid[r][c]
			v[idx] = cell.R / 255.0
			v[idx+1] = cell.G / 255.0
			v[idx+2] = cell.B / 255.0
			idx += 3
		}
	}
	return v
}
