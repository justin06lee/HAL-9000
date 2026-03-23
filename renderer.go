package main

import (
	"fmt"
	"math"
	"strings"
)

const (
	blocks     = " ·∙░░▒▒▓▓██"
	rSphere    = 1.5
	k2Const    = 5.0
	eyeCore    = 0.14
	eyeBright  = 0.24
	eyeIris    = 0.38
	eyeRing    = 0.52
	thetaSteps = 150
	phiSteps   = 300
)

var (
	blocksRunes []rune
	eyePos      = [3]float64{rSphere, 0, 0}
	lightP      [3]float64
	lightR      [3]float64
)

func init() {
	blocksRunes = []rune(blocks)
	lightP = vecNorm([3]float64{-0.3, -0.5, -0.7})
	lightR = vecNorm([3]float64{0.6, 0.2, 0.5})
}

func vecNorm(v [3]float64) [3]float64 {
	l := math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
	if l < 1e-9 {
		return [3]float64{}
	}
	return [3]float64{v[0] / l, v[1] / l, v[2] / l}
}

func dot3(a, b [3]float64) float64 {
	return a[0]*b[0] + a[1]*b[1] + a[2]*b[2]
}

func rotY(p [3]float64, a float64) [3]float64 {
	c, s := math.Cos(a), math.Sin(a)
	return [3]float64{c*p[0] + s*p[2], p[1], -s*p[0] + c*p[2]}
}

func rotX(p [3]float64, a float64) [3]float64 {
	c, s := math.Cos(a), math.Sin(a)
	return [3]float64{p[0], c*p[1] - s*p[2], s*p[1] + c*p[2]}
}

// lerpAngle interpolates between two angles via the shortest arc.
func lerpAngle(a, b, t float64) float64 {
	diff := math.Mod(b-a+3*math.Pi, 2*math.Pi) - math.Pi
	return a + diff*t
}

func easeInOut(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	return 1 - math.Pow(-2*t+2, 3)/2
}

const (
	gazeHoldTime  = 8.0  // seconds to hold gaze at camera
	gazeBlendTime = 1.5  // seconds to smoothly transition in/out
)

func renderEye(cols, rows int, timeS, cycle, sinceSpeech float64) string {
	nChars := len(blocksRunes)
	k1 := float64(rows) * 0.32 * k2Const / rSphere

	size := rows * cols
	buf := make([]int, size)
	zbuf := make([]float64, size)
	zone := make([]int, size)
	lumV := make([]float64, size)

	for i := range zbuf {
		zbuf[i] = -1e9
	}

	// Normal spinning rotation
	cyclePos := math.Mod(timeS, cycle) / cycle
	spinB := easeInOut(cyclePos)*math.Pi*2 + math.Pi/2

	// Face-camera angle (Pi/2 faces camera due to the eye position on +X axis)
	faceB := math.Pi / 2

	// Blend: smoothly lerp toward camera when HAL speaks, hold, then ease back out
	var rotB float64
	if sinceSpeech < gazeBlendTime {
		// Easing IN to face camera
		t := easeInOut(sinceSpeech / gazeBlendTime)
		rotB = lerpAngle(spinB, faceB, t)
	} else if sinceSpeech < gazeHoldTime {
		// Holding gaze at camera
		rotB = faceB
	} else if sinceSpeech < gazeHoldTime+gazeBlendTime {
		// Easing OUT back to spinning
		t := easeInOut((sinceSpeech - gazeHoldTime) / gazeBlendTime)
		rotB = lerpAngle(faceB, spinB, t)
	} else {
		// Normal spinning
		rotB = spinB
	}

	rotA := 0.18 * math.Sin(timeS*0.5)
	pulseVal := 0.5 + 0.5*math.Sin(timeS*2.5)

	for it := 0; it <= thetaSteps; it++ {
		theta := float64(it) / thetaSteps * math.Pi
		sinT, cosT := math.Sin(theta), math.Cos(theta)

		for ip := 0; ip < phiSteps; ip++ {
			phi := float64(ip) / phiSteps * 2 * math.Pi
			sinP, cosP := math.Sin(phi), math.Cos(phi)

			x := rSphere * sinT * cosP
			y := rSphere * cosT
			z := rSphere * sinT * sinP

			p := rotX([3]float64{x, y, z}, rotA)
			p = rotY(p, rotB)
			n := rotX([3]float64{sinT * cosP, cosT, sinT * sinP}, rotA)
			n = rotY(n, rotB)

			depth := p[2] + k2Const
			if depth < 0.3 {
				continue
			}
			ooz := 1.0 / depth

			xp := int(float64(cols)/2 + k1*ooz*p[0]*2)
			yp := int(float64(rows)/2 - k1*ooz*p[1])

			if xp < 0 || xp >= cols || yp < 0 || yp >= rows {
				continue
			}

			idx := yp*cols + xp
			if ooz <= zbuf[idx] {
				continue
			}
			zbuf[idx] = ooz

			// Back-face cull
			if n[2] > 0.08 {
				buf[idx] = 0
				zone[idx] = 0
				lumV[idx] = 0
				continue
			}

			// Lighting
			diffuse := dot3(n, lightP)
			rim := dot3(n, lightR)
			lum := math.Max(0, diffuse) + math.Max(0, rim)*0.18
			lum = math.Min(1, lum+0.05)

			// Eye distance zones
			dx := x - eyePos[0]
			dy := y - eyePos[1]
			dz := z - eyePos[2]
			dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

			zID := 1
			if dist < eyeCore {
				zID = 5
				lum = 0.92 + 0.08*pulseVal
			} else if dist < eyeBright {
				zID = 4
				tLerp := (dist - eyeCore) / (eyeBright - eyeCore)
				lum = 0.55 + 0.40*(1-tLerp) + 0.05*pulseVal
			} else if dist < eyeIris {
				zID = 3
				lum = 0.22 + 0.28*lum
			} else if dist < eyeRing {
				zID = 2
				lum = 0.10 + 0.20*lum
			}

			ci := int(lum * float64(nChars-1))
			ci = max(0, min(ci, nChars-1))
			buf[idx] = ci
			zone[idx] = zID
			lumV[idx] = lum
		}
	}

	// Build output string with ANSI true-color
	var sb strings.Builder
	sb.Grow(size * 20)

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			idx := y*cols + x
			ci := buf[idx]
			ch := blocksRunes[ci]

			if ch == ' ' {
				sb.WriteByte(' ')
				continue
			}

			zID := zone[idx]
			var r, g, b int
			switch zID {
			case 5:
				r = int(230 + 25*pulseVal)
				g = int(55 + 50*pulseVal)
				b = int(5 + 20*pulseVal)
			case 4:
				r = int(180 + 50*pulseVal)
				g = int(25 + 25*pulseVal)
				b = 8
			case 3:
				r = int(95 + 35*pulseVal)
				g = 14
				b = 6
			case 2:
				r = int(50 + 18*pulseVal)
				g = 10
				b = 6
			default:
				v := lumV[idx]
				r = int(25 + 80*v)
				g = int(35 + 90*v)
				b = int(50 + 120*v)
			}

			fmt.Fprintf(&sb, "\033[38;2;%d;%d;%dm%c", r, g, b, ch)
		}
		sb.WriteString("\033[0m")
		if y < rows-1 {
			sb.WriteByte('\n')
		}
	}

	return sb.String()
}
