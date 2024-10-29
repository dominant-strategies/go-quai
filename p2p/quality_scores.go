package p2p

const MinScore = 0
const MaxScore = 1000

func boundedAdj(current int, adj int) int {
	current += adj
	if current > MaxScore {
		current = MaxScore
	} else if current < MinScore {
		current = MinScore
	}
	return current
}

func QualityAdjOnBroadcast(current int) int {
	return boundedAdj(current, 1)
}
func QualityAdjOnResponse(current int) int {
	return boundedAdj(current, 10)
}
func QualityAdjOnNack(current int) int {
	return boundedAdj(current, -10)
}
func QualityAdjOnTimeout(current int) int {
	return boundedAdj(current, -20)
}
func QualityAdjOnBadResponse(curent int) int {
	return boundedAdj(curent, -40)
}
