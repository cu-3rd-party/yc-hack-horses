// Package reid computes visual descriptors and separate measurements of visible regions.
package reid

import (
	"math"
	"sort"
)

const traitCount = 5

type measure struct {
	value   float64
	visible bool
}

func observed(v float64) measure { return measure{v, true} }

type pixel struct {
	x, y                     float64 // original image coordinates
	luma, warmth             float64
	body, head, ears, muzzle bool
}

type region struct{ light, warm []float64 }

func (r *region) add(p pixel) {
	r.light = append(r.light, p.luma)
	r.warm = append(r.warm, p.warmth)
}

func quantile(a []float64, q float64) float64 {
	sort.Float64s(a)
	return a[int(math.Round(q*float64(len(a)-1)))]
}

type sampleRegion uint8

const (
	unmeasured sampleRegion = iota
	coatRegion
	cheekRegion
	muzzleRegion
)

// colorRegion is shared by the descriptor and its visual explanation.
func colorRegion(p pixel, width, height float64) sampleRegion {
	if !p.body || p.luma < .04 || p.luma > .96 {
		return unmeasured
	}
	switch {
	case p.head && p.muzzle:
		return muzzleRegion
	case p.head && !p.ears:
		return cheekRegion
	case !p.head && !p.ears && !p.muzzle && p.x >= .25*width && p.x <= .75*width && p.y >= .25*height && p.y <= .60*height:
		return coatRegion
	}
	return unmeasured
}

// Regions contain only segmented pixels. Mid-body excludes head, ears and muzzle.
// The bright muzzle quantile reduces the influence of dark nostrils in that mask.
func measurements(samples []pixel, boxWidth, boxHeight float64) [traitCount]measure {
	var traits [traitCount]measure
	var body, cheek, muzzle region
	var headCount int
	var sx, sy, xx, yy, xy, earX, earY, muzzleX, muzzleY float64
	var ears, tips int
	for _, p := range samples {
		if !p.body {
			continue
		}
		if p.ears {
			ears++
			earX += p.x
			earY += p.y
		}
		if p.muzzle {
			tips++
			muzzleX += p.x
			muzzleY += p.y
		}
		if p.head && !p.ears {
			headCount++
			sx += p.x
			sy += p.y
			xx += p.x * p.x
			yy += p.y * p.y
			xy += p.x * p.y
		}
		switch colorRegion(p, boxWidth, boxHeight) {
		case muzzleRegion:
			muzzle.add(p)
		case cheekRegion:
			cheek.add(p)
		case coatRegion:
			body.add(p)
		}
	}
	// At most one sample per native pixel; suppress tiny apparent regions.
	const minSamples = 24
	if len(body.light) >= minSamples {
		traits[0] = observed(quantile(body.light, .5))
		traits[1] = observed(quantile(body.warm, .5))
	}
	if len(cheek.light) >= minSamples {
		ref := quantile(cheek.light, .5)
		traits[2] = observed(ref)
		if len(muzzle.light) >= minSamples {
			traits[3] = observed(quantile(muzzle.light, .75) - ref)
		}
	}
	// Projected head breadth/length from second moments, gated on lateral separation
	// of ears and muzzle. No claim about forehead curvature from a frontal view.
	if headCount >= minSamples && ears >= minSamples && tips >= minSamples {
		n := float64(headCount)
		vx, vy, cov := xx/n-(sx/n)*(sx/n), yy/n-(sy/n)*(sy/n), xy/n-sx*sy/(n*n)
		spread := math.Hypot(vx-vy, 2*cov)
		major, minor := (vx+vy+spread)/2, (vx+vy-spread)/2
		dx, dy := earX/float64(ears)-muzzleX/float64(tips), earY/float64(ears)-muzzleY/float64(tips)
		// Require spatially distinct landmarks and lateral offset of at least one transverse deviation.
		if minor > 0 && math.Hypot(dx, dy) >= 1.5*math.Sqrt(major) && math.Abs(dx) >= math.Sqrt(minor) {
			traits[4] = observed(math.Sqrt(minor / major))
		}
	}
	return traits
}
