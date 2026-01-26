package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestIMDCTEnergyPreservation(t *testing.T) {
	// Test that IMDCT preserves energy appropriately
	// For MDCT/IMDCT with proper normalization:
	// E_out = E_in (Parseval's theorem)

	N := 480
	spectrum := make([]float64, N)
	for i := 0; i < N; i++ {
		spectrum[i] = math.Sin(float64(i)*0.1) * 0.1
	}

	// Input energy
	var inputEnergy float64
	for _, x := range spectrum {
		inputEnergy += x * x
	}

	// IMDCTDirect output
	output := IMDCTDirect(spectrum)

	// Output energy
	var outputEnergy float64
	for _, y := range output {
		outputEnergy += y * y
	}

	inputRMS := math.Sqrt(inputEnergy / float64(len(spectrum)))
	outputRMS := math.Sqrt(outputEnergy / float64(len(output)))

	fmt.Printf("Input: N=%d, Energy=%.6f, RMS=%.6f\n", len(spectrum), inputEnergy, inputRMS)
	fmt.Printf("Output: N=%d, Energy=%.6f, RMS=%.6f\n", len(output), outputEnergy, outputRMS)
	fmt.Printf("Energy ratio (out/in): %.6f\n", outputEnergy/inputEnergy)
	fmt.Printf("RMS ratio (out/in): %.6f\n", outputRMS/inputRMS)

	// For proper IMDCT normalization:
	// If we use 2/N scaling, output energy should be 4*N/(N^2) * input = 4/N * input
	// So energy ratio should be 4/N = 0.0083 for N=480
	expectedRatio := 4.0 / float64(N)
	fmt.Printf("Expected ratio (4/N): %.6f\n", expectedRatio)

	// Alternative: IMDCT often has 2/N normalization for proper reconstruction
	// With overlap-add of 50%, the energy is preserved through MDCT->IMDCT->overlap-add

	// Test imdctOverlapWithPrev
	prevOverlap := make([]float64, Overlap)
	result := imdctOverlapWithPrev(spectrum, prevOverlap, Overlap)

	var resultEnergy float64
	for _, y := range result[:N] { // Only output portion
		resultEnergy += y * y
	}
	resultRMS := math.Sqrt(resultEnergy / float64(N))

	fmt.Printf("\nimdctOverlapWithPrev:\n")
	fmt.Printf("Output (N samples): Energy=%.6f, RMS=%.6f\n", resultEnergy, resultRMS)
	fmt.Printf("Energy ratio: %.6f\n", resultEnergy/inputEnergy)
}

func TestSynthesisEnergyPreservation(t *testing.T) {
	// Test full synthesis energy preservation
	dec := NewDecoder(1)

	// Create test coefficients with known energy
	frameSize := 960
	coeffs := make([]float64, frameSize)
	for i := range coeffs {
		coeffs[i] = math.Sin(float64(i)*0.1) * 0.1
	}

	// Input energy and RMS
	var inputEnergy float64
	for _, x := range coeffs {
		inputEnergy += x * x
	}
	inputRMS := math.Sqrt(inputEnergy / float64(len(coeffs)))

	fmt.Printf("Input coefficients: RMS=%.6f\n", inputRMS)

	// Non-transient synthesis
	samples := dec.Synthesize(coeffs, false, 1)

	var outputEnergy float64
	for _, s := range samples {
		outputEnergy += s * s
	}
	outputRMS := math.Sqrt(outputEnergy / float64(len(samples)))

	fmt.Printf("Output samples (non-transient): RMS=%.6f\n", outputRMS)
	fmt.Printf("RMS ratio: %.6f\n", outputRMS/inputRMS)

	// Transient synthesis
	dec2 := NewDecoder(1)
	transientSamples := dec2.Synthesize(coeffs, true, 8)

	var transientEnergy float64
	for _, s := range transientSamples {
		transientEnergy += s * s
	}
	transientRMS := math.Sqrt(transientEnergy / float64(len(transientSamples)))

	fmt.Printf("Output samples (transient, 8 blocks): RMS=%.6f\n", transientRMS)
	fmt.Printf("RMS ratio: %.6f\n", transientRMS/inputRMS)
}
