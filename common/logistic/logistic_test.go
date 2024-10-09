package logistic

import (
	"fmt"
	"math/big"
	"testing"
)

func TestLogistic(t *testing.T) {
	r := NewLogisticRegression()

	X := []*big.Float{big.NewFloat(1), big.NewFloat(2), big.NewFloat(3), big.NewFloat(4), big.NewFloat(10), big.NewFloat(11), big.NewFloat(12), big.NewFloat(13)}
	Y := []*big.Float{big.NewFloat(0), big.NewFloat(0), big.NewFloat(0), big.NewFloat(0), big.NewFloat(1), big.NewFloat(1), big.NewFloat(1), big.NewFloat(1)}

	r.Train(X, Y)

	fmt.Println("Beta0/1 after training", r.Beta0(), r.Beta1())

	xValues := make([]float64, 0)
	for _, x := range X {
		xF, _ := x.Float64()
		xValues = append(xValues, xF)
	}

	yValues := make([]float64, 0)
	for _, y := range Y {
		yF, _ := y.Float64()
		yValues = append(yValues, yF)
	}

	r.PlotSigmoid(xValues, yValues)
}
