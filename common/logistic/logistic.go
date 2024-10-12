package logistic

import (
	"fmt"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

var (
	c_learningRate = big.NewFloat(0.001)
	c_epochLength  = 1000
)

// LogisticRegression represents a logistic regression model.
type LogisticRegression struct {
	beta0 *big.Float // Model bias (intercept)
	beta1 *big.Float // Model weight (slope)
}

// NewLogisticRegression initializes a new LogisticRegression model.
func NewLogisticRegression(beta0, beta1 *big.Float) *LogisticRegression {
	return &LogisticRegression{
		beta0: big.NewFloat(0.1),
		beta1: big.NewFloat(0.000081),
	}
}

// sigmoid computes the sigmoid function.
func sigmoid(z *big.Float) *big.Float {
	// Compute exp(-z)
	negZ := new(big.Float).Neg(z)
	expNegZ := math.EToTheX(negZ)

	// Compute 1 + exp(-z)
	denom := new(big.Float).Add(new(big.Float).SetInt(common.Big1), expNegZ)

	// Compute 1 / (1 + exp(-z))
	result := new(big.Float).Quo(new(big.Float).SetInt(common.Big1), denom)

	return result
}

// Predict computes the probability that the input belongs to class 1.
func (lr *LogisticRegression) Predict(x *big.Float) *big.Float {
	// z = beta0 + beta1 * x
	beta1x := new(big.Float).Mul(lr.beta1, x)
	z := new(big.Float).Add(lr.beta0, beta1x)

	// Apply sigmoid function
	return sigmoid(z)
}

// Train trains the logistic regression model using gradient descent.
func (lr *LogisticRegression) Train(x []*big.Int, y []*big.Int) {
	nSamples := len(y)

	var xfloat, yfloat []*big.Float
	for i := 0; i < nSamples; i++ {
		xfloat = append(xfloat, new(big.Float).SetInt(x[i]))
		yfloat = append(yfloat, new(big.Float).SetInt(y[i]))
	}

	for epoch := 0; epoch < c_epochLength; epoch++ {
		// Initialize gradients
		dw := new(big.Float).SetInt(common.Big0)
		db := new(big.Float).SetInt(common.Big0)

		// Compute gradients
		for i := 0; i < nSamples; i++ {
			xi := xfloat[i]
			yi := yfloat[i]
			pred := lr.Predict(xi)
			error := new(big.Float).Sub(pred, yi)
			dwTerm := new(big.Float).Mul(error, xi)
			dw.Add(dw, dwTerm)
			db.Add(db, error)
		}

		nSamplesFloat := new(big.Float).SetInt(big.NewInt(int64(nSamples))) //big.NewFloat(float64(nSamples))

		// Compute gradient averages
		dw.Quo(dw, nSamplesFloat)
		db.Quo(db, nSamplesFloat)

		// Update weight: beta1 = beta1 - LearningRate * dw
		lrUpdateW := new(big.Float).Mul(c_learningRate, dw)
		lr.beta1.Sub(lr.beta1, lrUpdateW)

		// Update bias: beta0 = beta0 - LearningRate * db
		lrUpdateB := new(big.Float).Mul(c_learningRate, db)
		lr.beta0.Sub(lr.beta0, lrUpdateB)
	}
}

// Beta0 returns the model's bias (intercept) term.
func (lr *LogisticRegression) Beta0() *big.Float {
	return new(big.Float).Set(lr.beta0)
}

// Beta1 returns the model's weight (slope) term.
func (lr *LogisticRegression) Beta1() *big.Float {
	return new(big.Float).Set(lr.beta1)
}

// BigBeta0 returns the model's bias (intercept) term.
func (lr *LogisticRegression) BigBeta0() *big.Int {
	bigBeta := new(big.Float).Mul(lr.beta0, new(big.Float).SetInt(common.Big2e64))
	bigBetaInt, _ := bigBeta.Int(nil)
	return bigBetaInt
}

// BigBeta1 returns the model's weight (slope) term.
func (lr *LogisticRegression) BigBeta1() *big.Int {
	bigBeta := new(big.Float).Mul(lr.beta1, new(big.Float).SetInt(common.Big2e64))
	bigBetaInt, _ := bigBeta.Int(nil)
	return bigBetaInt
}

// Plot the given trained logistic regression values with Beta0 and Beta1
func (lr *LogisticRegression) PlotSigmoid(xValues, yValues []float64, blockNumber uint64) error {
	// Create a new plot
	p := plot.New()

	beta0, _ := lr.beta0.Float64()
	beta1, _ := lr.beta1.Float64()

	p.Title.Text = fmt.Sprintf("Sigmoid Function: Beta0=%.10f, Beta1=%.10f", beta0, beta1)
	p.X.Label.Text = "x"
	p.Y.Label.Text = "sigmoid(x)"

	plotValues := make(plotter.XYs, 0)
	for i := range xValues {
		value := plotter.XY{xValues[i], yValues[i]}
		plotValues = append(plotValues, value)
	}

	// Create a line plotter with x and y values
	line, err := plotter.NewLine(plotValues)
	if err != nil {
		return err
	}

	// Add the line to the plot
	p.Add(line)

	// Create the function to be plotted
	sigmoidFunc := plotter.NewFunction(func(x float64) float64 {
		result := lr.Predict(big.NewFloat(x))
		resultF, _ := result.Float64()
		return resultF
	})

	// Set the style for the function line
	sigmoidFunc.Color = plotter.DefaultLineStyle.Color
	sigmoidFunc.Width = vg.Points(2)

	// Set the range for x-axis values
	// Find the min and max in the xValues
	xMin := float64(math.MaxInt64)
	xMax := float64(0)
	for _, x := range xValues {
		if x < xMin {
			xMin = x
		} else if x > xMax {
			xMax = x
		}
	}
	sigmoidFunc.XMin = xMin
	sigmoidFunc.XMax = xMax

	p.Add(sigmoidFunc)

	// Save the plot as a PNG image
	if err := p.Save(6*vg.Inch, 4*vg.Inch, fmt.Sprintf("sigmoid-%d.png", blockNumber)); err != nil {
		return err
	}

	return nil
}
