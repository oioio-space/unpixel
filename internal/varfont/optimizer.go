package varfont

// OptimizerKind selects the search strategy used by [FitAxes].
//
// The default ([OptimizerCoordDescent]) is stable, fully deterministic, and
// efficient for single-axis or weakly-coupled landscapes. [OptimizerNelderMead]
// is a gradient-free simplex method that explores the full n-axis space
// simultaneously and can escape shallow local minima on coupled landscapes
// (e.g. wght+wdth where the optimum lies off the coordinate axes).
//
// Both strategies are deterministic: OptimizerNelderMead uses a fixed
// pseudo-random seed so results are reproducible across calls with the same
// [FitConfig].
type OptimizerKind uint8

const (
	// OptimizerCoordDescent is the default per-axis coordinate descent used
	// by the original [FitAxes] implementation. It is fast (~2·n probes per
	// round) and reliable when axes are independent.
	OptimizerCoordDescent OptimizerKind = iota

	// OptimizerNelderMead runs a Nelder-Mead downhill simplex over the full
	// n-axis parameter space. It uses more evaluations per round but is better
	// suited to coupled axes because it moves the entire simplex together
	// rather than one axis at a time.
	//
	// The simplex is seeded deterministically from the AxisSpec.Start values
	// so results are reproducible.  #nosec G404 — math/rand is intentional:
	// we need cheap, seedable, deterministic pseudo-randomness, not
	// cryptographic quality.
	OptimizerNelderMead
)

// nelderMead minimises f over an n-dimensional box defined by specs, starting
// from the current values in start (length == len(specs)). It returns the
// minimising point and the function value at that point.
//
// Parameters follow the classical Nelder-Mead constants:
//
//	α = 1.0  reflection
//	γ = 2.0  expansion
//	ρ = 0.5  contraction
//	σ = 0.5  shrink
//
// Termination: maxIter full simplex operations, or when the range of vertex
// function values falls below tol.
//
// The simplex is initialised deterministically: the first vertex is start;
// subsequent vertices perturb one coordinate at a time by initStep (clamped
// to spec bounds). This mirrors the coordinate-descent starting geometry.
//
// Thread safety: f is called synchronously; nelderMead itself is pure and
// therefore goroutine-safe when f is safe.
func nelderMead(
	specs []AxisSpec,
	start []float32,
	initStep float32,
	maxIter int,
	f func([]float32) (float64, error),
) (best []float32, bestVal float64, evals int, err error) {
	n := len(specs)
	// Number of simplex vertices = n+1.
	nv := n + 1

	// Initialise vertices: v[0] = start; v[i+1] perturbs axis i by initStep.
	v := make([][]float32, nv)
	for i := range nv {
		v[i] = make([]float32, n)
		copy(v[i], start)
	}
	for i := range n {
		perturbed := clampAxis(start[i]+initStep, specs[i].Min, specs[i].Max)
		if perturbed == start[i] {
			// At max boundary — try perturbing down instead.
			perturbed = clampAxis(start[i]-initStep, specs[i].Min, specs[i].Max)
		}
		v[i+1][i] = perturbed
	}

	// Evaluate all initial vertices.
	fv := make([]float64, nv)
	for i := range nv {
		fv[i], err = f(v[i])
		if err != nil {
			return nil, 0, evals, err
		}
		evals++
	}

	// Nelder-Mead constants.
	const (
		alpha = 1.0 // reflection
		gamma = 2.0 // expansion
		rho   = 0.5 // contraction
		sigma = 0.5 // shrink
		tol   = 1e-6
	)

	// Scratch space for centroid and candidate points — allocated once.
	centroid := make([]float32, n)
	reflected := make([]float32, n)
	expanded := make([]float32, n)
	contracted := make([]float32, n)

	// sortVertices sorts v and fv so that v[0] is the best (lowest fv[0]).
	sortVertices := func() {
		// Insertion sort — nv is at most n+1 ≤ ~5, so O(n²) is fine.
		for i := 1; i < nv; i++ {
			for j := i; j > 0 && fv[j] < fv[j-1]; j-- {
				v[j], v[j-1] = v[j-1], v[j]
				fv[j], fv[j-1] = fv[j-1], fv[j]
			}
		}
	}

	// computeCentroid computes the centroid of the n best vertices (all but
	// the worst, v[nv-1]).
	computeCentroid := func() {
		clear(centroid)
		for i := range nv - 1 {
			for d := range n {
				centroid[d] += v[i][d]
			}
		}
		denom := float32(nv - 1)
		for d := range n {
			centroid[d] /= denom
		}
	}

	// clampVec clamps all elements of dst to their axis bounds and writes into dst.
	clampVec := func(dst []float32) {
		for d, s := range specs {
			dst[d] = clampAxis(dst[d], s.Min, s.Max)
		}
	}

	// reflect computes the reflected point: c + α*(c - worst).
	reflect := func() {
		for d := range n {
			reflected[d] = centroid[d] + alpha*(centroid[d]-v[nv-1][d])
		}
		clampVec(reflected)
	}

	// expand computes the expanded point: c + γ*(reflected - c).
	expand := func() {
		for d := range n {
			expanded[d] = centroid[d] + gamma*(reflected[d]-centroid[d])
		}
		clampVec(expanded)
	}

	// contractOutside: c + ρ*(reflected - c).
	contractOutside := func() {
		for d := range n {
			contracted[d] = centroid[d] + rho*(reflected[d]-centroid[d])
		}
		clampVec(contracted)
	}

	// contractInside: c + ρ*(worst - c).
	contractInside := func() {
		for d := range n {
			contracted[d] = centroid[d] + rho*(v[nv-1][d]-centroid[d])
		}
		clampVec(contracted)
	}

	// shrinkAll shrinks all vertices toward the best (v[0]).
	shrinkAll := func() error {
		for i := 1; i < nv; i++ {
			for d := range n {
				v[i][d] = v[0][d] + sigma*(v[i][d]-v[0][d])
			}
			clampVec(v[i])
			fv[i], err = f(v[i])
			if err != nil {
				return err
			}
			evals++
		}
		return nil
	}

	sortVertices()

	for range maxIter {
		// Convergence check: range of function values.
		if fv[nv-1]-fv[0] < tol {
			break
		}

		computeCentroid()
		reflect()

		fr, evalErr := f(reflected)
		if evalErr != nil {
			return nil, 0, evals, evalErr
		}
		evals++

		switch {
		case fr < fv[0]:
			// Reflected is better than best — try expanding.
			expand()
			fe, evalErr := f(expanded)
			if evalErr != nil {
				return nil, 0, evals, evalErr
			}
			evals++
			if fe < fr {
				copy(v[nv-1], expanded)
				fv[nv-1] = fe
			} else {
				copy(v[nv-1], reflected)
				fv[nv-1] = fr
			}

		case fr < fv[nv-2]:
			// Reflected is better than the second-worst — accept it.
			copy(v[nv-1], reflected)
			fv[nv-1] = fr

		default:
			// Reflected is not better than second-worst — contract.
			if fr < fv[nv-1] {
				contractOutside()
			} else {
				contractInside()
			}
			fc, evalErr := f(contracted)
			if evalErr != nil {
				return nil, 0, evals, evalErr
			}
			evals++
			if fc < fv[nv-1] {
				copy(v[nv-1], contracted)
				fv[nv-1] = fc
			} else {
				// Shrink all toward best.
				if shrinkErr := shrinkAll(); shrinkErr != nil {
					return nil, 0, evals, shrinkErr
				}
			}
		}

		sortVertices()
	}

	// v[0] is the best vertex after sorting.
	result := make([]float32, n)
	copy(result, v[0])
	return result, fv[0], evals, nil
}
